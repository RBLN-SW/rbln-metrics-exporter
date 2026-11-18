package host

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// PCIIDs contains unmodified product names, including any PF/VF suffix. Label
// escaping and model/resource naming are consumer policy.
type PCIIDs struct{ names map[uint16]string }

// FindPCIIDsPath locates the distribution's database in the current namespace.
// Images may instead provide an explicit bundled database to LoadPCIIDs.
func FindPCIIDsPath() (string, error) {
	return FindPCIIDsIn([]string{"/usr/share/misc/pci.ids", "/usr/share/hwdata/pci.ids", "/var/lib/pciutils/pci.ids"})
}

func FindPCIIDsIn(paths []string) (string, error) {
	for _, p := range paths {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", failure("pci.ids", p, err)
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return p, nil
	}
	return "", &system.Error{Kind: system.NotFound, Message: "pci.ids database is not installed"}
}

func (p *PCIIDs) Lookup(id uint16) string {
	if p == nil {
		return ""
	}
	return p.names[id]
}

func (p *PCIIDs) Len() int {
	if p == nil {
		return 0
	}
	return len(p.names)
}
func (p *PCIIDs) LookupHex(raw string) string {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "0x"), 16, 16)
	if err != nil {
		return ""
	}
	return p.Lookup(uint16(n))
}

func LoadPCIIDs(path string) (*PCIIDs, error) {
	return LoadVendorPCIIDs(path, fmt.Sprintf("%04x", system.VendorID))
}

func LoadVendorPCIIDs(path, vendor string) (*PCIIDs, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, failure("pci.ids", path, err)
	}
	defer func() { _ = f.Close() }()
	return ParseVendorPCIIDs(f, vendor)
}

func ParsePCIIDs(r io.Reader) (*PCIIDs, error) {
	return ParseVendorPCIIDs(r, fmt.Sprintf("%04x", system.VendorID))
}

func ParseVendorPCIIDs(r io.Reader, vendor string) (*PCIIDs, error) {
	if len(vendor) != 4 {
		return nil, &system.Error{Kind: system.InvalidArgument, Target: vendor, Message: "vendor must be four hex digits"}
	}
	if _, err := strconv.ParseUint(vendor, 16, 16); err != nil {
		return nil, &system.Error{Kind: system.InvalidArgument, Target: vendor, Cause: err}
	}
	p := &PCIIDs{names: map[uint16]string{}}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 1024*1024)
	inVendor := false
	for s.Scan() {
		line := s.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			if inVendor {
				break
			}
			parts := strings.Fields(line)
			inVendor = len(parts) > 0 && strings.EqualFold(parts[0], vendor)
			continue
		}
		if !inVendor || strings.HasPrefix(line, "\t\t") {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			continue
		}
		id, err := strconv.ParseUint(line[:i], 16, 16)
		if err != nil {
			continue
		}
		if name := strings.TrimSpace(line[i:]); name != "" {
			p.names[uint16(id)] = name
		}
	}
	return p, s.Err()
}
