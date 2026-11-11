package daemon

import pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"

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
	SID                 string
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
	DevState            pb.DeviceState
	CollectionSucceeded bool
	PState              *int64
	PCIeLinkSpeed       *float64
	PCIeLinkWidth       *int64
	Topology            *TopologyInfo
}

func (d DeviceInfo) HasKnownState() bool {
	switch d.DevState {
	case pb.DeviceState_DEVICE_STATE_FAULT, pb.DeviceState_DEVICE_STATE_INIT, pb.DeviceState_DEVICE_STATE_READY,
		pb.DeviceState_DEVICE_STATE_BUSY, pb.DeviceState_DEVICE_STATE_FINISH:
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
	if d.DevState == pb.DeviceState_DEVICE_STATE_READY {
		return 0, true
	}
	return 1, true
}
