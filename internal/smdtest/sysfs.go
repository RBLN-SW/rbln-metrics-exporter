package smdtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func PCIEntry(t testing.TB, root, bus, driver string) {
	t.Helper()
	dir := filepath.Join(root, bus)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for attr, value := range map[string]string{"vendor": "0x1eff", "class": "0x120000", "device": "0x2130"} {
		if err := os.WriteFile(filepath.Join(dir, attr), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if driver != "" {
		target := filepath.Join(root, ".drivers", driver)
		if err := os.MkdirAll(target, 0755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "driver")
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
}

// Partition models reciprocal Linux PCI links, including unobserved siblings.
func Partition(t testing.TB, root, pf, mode string) {
	t.Helper()
	PCIEntry(t, root, pf, "rebellions")
	if err := os.WriteFile(filepath.Join(root, pf, "sriov_numvfs"), []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(mode)
	if err != nil || n < 0 {
		return
	}
	links, err := filepath.Glob(filepath.Join(root, pf, "virtfn*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		vf := strings.TrimSuffix(pf, ".0") + fmt.Sprintf(".%d", i+1)
		PCIEntry(t, root, vf, "rebellions")
		parent := filepath.Join(root, vf, "physfn")
		if err := os.Remove(parent); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, pf), parent); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, vf), filepath.Join(root, pf, fmt.Sprintf("virtfn%d", i))); err != nil {
			t.Fatal(err)
		}
	}
}
