package host

import (
	_ "embed"
	"strings"
	"sync"
)

// Bundled vendor identifiers support VFIO inventory where native metadata is
// unavailable. Product/resource naming policy stays with each consumer.
//
//go:embed rebellions-pci.ids
var bundledPCIIDs string
var loadBuiltinPCIIDs = sync.OnceValues(func() (*PCIIDs, error) { return ParsePCIIDs(strings.NewReader(bundledPCIIDs)) })

func BuiltinPCIIDs() (*PCIIDs, error) { return loadBuiltinPCIIDs() }
