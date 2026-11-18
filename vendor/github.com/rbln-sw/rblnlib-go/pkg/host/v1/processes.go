package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

type Process struct {
	PID     int
	Command string
}
type Usage struct {
	PCIAddress string
	Holders    []Process
}

// DeviceUsage scans the configured host procfs. Permission failures are not
// mistaken for no holders. This is a point-in-time observation, not exclusion
// of future opens; orchestration must stop new users before changing bindings.
func (c *Client) DeviceUsage(ctx context.Context, addresses []string) ([]Usage, error) {
	if err := system.ValidTargets(addresses); err != nil {
		return nil, err
	}
	owners := map[string][]int{}
	deviceNumbers := map[[2]uint32][]int{}
	out := make([]Usage, len(addresses))
	for i, address := range addresses {
		a, err := system.NormalizePCIAddress(address)
		if err != nil {
			return nil, err
		}
		out[i].PCIAddress = a
		driver, err := c.CurrentDriver(ctx, a)
		if err != nil {
			return nil, err
		}
		if driver == "" {
			continue
		}
		nodes, err := c.DeviceNodes(ctx, a)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if n.Kind == "vfio-control" {
				continue
			} // shared control fd alone identifies no group
			owners[n.Path] = append(owners[n.Path], i)
			if n.HostPath != n.Path {
				owners[n.HostPath] = append(owners[n.HostPath], i)
			}
			if n.Major != nil && n.Minor != nil {
				key := [2]uint32{*n.Major, *n.Minor}
				deviceNumbers[key] = append(deviceNumbers[key], i)
			}
		}
	}
	if len(owners) == 0 {
		return out, nil
	}
	entries, err := os.ReadDir(c.opts.ProcRoot)
	if err != nil {
		return nil, failure("procfs", c.opts.ProcRoot, err)
	}
	seen := map[[2]int]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		p := filepath.Join(c.opts.ProcRoot, e.Name())
		fds, err := os.ReadDir(filepath.Join(p, "fd"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		} // process exited
		if err != nil {
			return nil, failure("process descriptors", p, err)
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(p, "fd", fd.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue
			} // descriptor closed
			if err != nil {
				return nil, failure("process descriptor", p, err)
			}
			matches := owners[strings.TrimSuffix(target, " (deleted)")]
			if len(matches) == 0 {
				// dev_t also recognizes alternate paths/symlinks to the same node.
				var st unix.Stat_t
				if err := unix.Stat(filepath.Join(p, "fd", fd.Name()), &st); err == nil {
					if st.Mode&unix.S_IFMT == unix.S_IFCHR {
						matches = deviceNumbers[[2]uint32{unix.Major(uint64(st.Rdev)), unix.Minor(uint64(st.Rdev))}]
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, failure("process descriptor stat", p, err)
				}
			}
			for _, index := range matches {
				key := [2]int{index, pid}
				if seen[key] {
					continue
				}
				seen[key] = true
				comm, _ := readText(filepath.Join(p, "comm"))
				out[index].Holders = append(out[index].Holders, Process{PID: pid, Command: comm})
			}
		}
	}
	for i := range out {
		sort.Slice(out[i].Holders, func(a, b int) bool { return out[i].Holders[a].PID < out[i].Holders[b].PID })
	}
	return out, nil
}
