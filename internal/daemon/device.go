package daemon

import system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"

type TopologyInfo struct {
	NUMANode  int32
	CPUList   string
	RSDGroup  string
	LinkSpeed *float64
	LinkWidth *int64
}

// Nil readings are unavailable, not zero. PFs hosting VFs remain observable
// physical devices, but their resources must not be counted as compute.
type DeviceInfo struct {
	UUID                string
	Name                string
	DeviceID            string
	Card                string
	PCIBusID            string
	IsVF                bool
	ParentName          string
	NumVFs              *uint32
	PartitionMode       string
	Compute             bool
	Temperature         *float64
	Power               *float64
	MemoryUsedBytes     *uint64
	MemoryTotalBytes    *uint64
	Utilization         *float64
	DriverVersion       string
	FirmwareVersion     string
	SMCVersion          string
	DevState            system.State
	CollectionSucceeded bool
	PState              *int64
	PCIeLinkSpeed       *float64
	PCIeLinkWidth       *int64
	Topology            *TopologyInfo
}

func (d DeviceInfo) HasKnownState() bool {
	switch d.DevState {
	case system.StateFault, system.StateInit, system.StateReady,
		system.StateBusy, system.StateFinish:
		return true
	default:
		return false
	}
}

// The original SMD health signal was READY=0, other known states=1.
// It is independent of whether the telemetry queries succeeded.
func (d DeviceInfo) Health() (float64, bool) {
	if !d.HasKnownState() {
		return 0, false
	}
	if d.DevState == system.StateReady {
		return 0, true
	}
	return 1, true
}
