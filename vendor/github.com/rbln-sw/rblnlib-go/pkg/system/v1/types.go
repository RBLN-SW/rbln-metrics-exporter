package system

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const NativeDriver = "rebellions"
const VFIODriver = "vfio-pci"
const VendorID uint16 = 0x1eff
const NPUClass uint32 = 0x120000

var pciAddressPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,8}:[0-9a-fA-F]{2}:[0-1][0-9a-fA-F]\.[0-7]$`)

// NormalizePCIAddress validates a full domain:bus:slot.function address.
func NormalizePCIAddress(address string) (string, error) {
	if !pciAddressPattern.MatchString(address) {
		return "", &Error{Kind: InvalidArgument, Op: "PCI address", Target: address}
	}
	return strings.ToLower(address), nil
}

type State string

// StateObservation distinguishes a numeric but unrecognized lifecycle value
// from an unreadable attribute. RawCode is diagnostic, not allocation policy.
type StateObservation struct {
	State   State
	RawCode *int64
}

func (s StateObservation) String() string {
	if s.State == StateUnknown && s.RawCode != nil {
		return fmt.Sprintf("UNKNOWN(%d)", *s.RawCode)
	}
	return strings.ToUpper(string(s.State))
}

const (
	StateUnknown State = "unknown"
	StateReady   State = "ready"
	StateBusy    State = "busy"
	StateProbing State = "probing"
	StateInit    State = "init"
	StateFault   State = "fault"
	StateFinish  State = "finish"
)

type FunctionKind string

const (
	FunctionUnknown  FunctionKind = "unknown"
	PhysicalFunction FunctionKind = "pf"
	VirtualFunction  FunctionKind = "vf"
)

type Versions struct{ Driver, Firmware, SMC string }

// DeviceNode describes an actual node. Path is the host's canonical /dev path;
// HostPath accounts for a configured host filesystem mount. Neither is an ID.
// Container mapping, permissions and ownership are consumer policy.
type DeviceNode struct {
	Path, HostPath string
	Major, Minor   *uint32
	Kind           string // native, vfio-control, vfio-group, rsd, rds
}

// SRIOV separates enabled children of this function from its parent's count.
// Supported=false means sriov_numvfs is absent on an existing PF. Missing or
// inconsistent topology is an error, never an unpartitioned PF.
type SRIOV struct {
	Kind             FunctionKind
	Supported        bool
	ParentPCIAddress string
	EnabledVFs       uint32
	ParentEnabledVFs *uint32
	TotalVFs         *uint32
	VirtualFunctions []string // ordered by virtfn index
}

type Topology struct {
	NUMANode                                      *int // nil includes Linux's -1 (no known affinity)
	CPUList                                       string
	PCIeRoot                                      string   // pci<domain>:<bus>, not the first downstream bridge
	PCIAncestors                                  []string // root through endpoint, canonical sysfs paths
	IOMMUGroup                                    *uint32
	IOMMUMembers                                  []string
	LinkSpeedGTPerSecond, MaxLinkSpeedGTPerSecond *float64
	LinkWidth, MaxLinkWidth                       *uint32
	RSDGroup                                      string
}

// ReportedPCI retains the spelling supplied by a metadata backend for existing
// selectors and labels. Use the normalized Device and Topology fields for
// comparisons. Nil optional strings use normalized formatting when projected
// into the legacy metadata shape. A reported empty string is preserved.
type ReportedPCI struct {
	Address, DeviceID              string
	NUMANode, LinkSpeed, LinkWidth *string
}

type Device struct {
	PCIAddress                   string
	Name                         string // backend/driver function name; never a path
	UUID, Serial, ProductName    string
	VendorID, DeviceID           uint16
	DeviceIDKnown                bool // true when DeviceID was read successfully, including zero
	ReportedPCI                  *ReportedPCI
	Class                        uint32
	Driver                       string // empty and DriverKnown=true means unbound
	DriverKnown                  bool
	DriverOverride               *string
	FunctionKind                 FunctionKind
	ParentName, ParentPCIAddress string
	State                        State
	Versions                     Versions
	MemoryTotalBytes             *uint64
	SRIOV                        *SRIOV
	// EnabledVFs is an optional backend observation, which may be cached.
	// SRIOV, when present, additionally carries a validated local topology.
	EnabledVFs  *uint32
	Topology    Topology
	Nodes       []DeviceNode
	Compute     *bool   // nil when the backend cannot determine compute capability
	Index       *uint32 // backend ordinal, valid for the current observation only
	NativeIndex *uint32 // driver's PF ordinal, independent of Index and node numbers
	Issues      []Issue
}

type Snapshot struct {
	Devices    []Device
	Issues     []Issue // failed entries which could not become a Device
	ObservedAt time.Time
	Source     string
}

// Err joins entry and field failures without discarding successful devices.
func (s Snapshot) Err() error {
	issues := append([]Issue(nil), s.Issues...)
	for _, d := range s.Devices {
		issues = append(issues, d.Issues...)
	}
	return IssuesError(issues)
}

type Inventory interface {
	ListDevices(context.Context) (Snapshot, error)
}
type DeviceReader interface {
	GetDevice(context.Context, string) (Device, error)
}
type StateReader interface {
	ReadState(context.Context, string) (State, error)
}
type NodeResolver interface {
	DeviceNodes(context.Context, string) ([]DeviceNode, error)
}

type Metric string

const (
	TemperatureCelsius Metric = "temperature_celsius"
	PowerWatts         Metric = "power_watts"
	UtilizationPercent Metric = "utilization_percent"
	MemoryUsedBytes    Metric = "memory_used_bytes"
	PerformanceState   Metric = "performance_state"
)

// MetricValue uses exactly one representation. Byte counts remain integers
// above 2^53. Absent in-scope v1 samples carry ErrNotReported on the metric
// field. An unreachable device carries a device-level issue instead. Metrics
// outside the scope of a function can be absent without an issue.
type MetricValue struct {
	Float *float64
	Uint  *uint64
}
type Telemetry struct {
	Device Device
	Values map[Metric]MetricValue
	Issues []Issue
}
type TelemetrySnapshot struct {
	Devices    []Telemetry
	Issues     []Issue
	ObservedAt time.Time
	Source     string
}

// CommandOptions IDs are passed through. A non-empty idempotency key identifies
// one logical request; reconcile attempts against new observations use a new
// key. A client never transparently replays a mutating operation.
type CommandOptions struct {
	RequestID, IdempotencyKey string
	Timeout                   time.Duration
	PerTargetTimeout          time.Duration // VF provisioning wait per PF; zero uses 300s
}
type Outcome struct {
	Target string
	Err    error
}
type CommandResult struct {
	AuditID  string
	Outcomes []Outcome
	// Indeterminate means some effect may have happened without a verified
	// response. Do not interpret it as a failed, safe-to-repeat command.
	Indeterminate bool
}

func (r CommandResult) Err() error {
	issues := make([]Issue, 0, len(r.Outcomes)+1)
	for _, o := range r.Outcomes {
		issues = append(issues, Issue{Target: o.Target, Err: o.Err})
	}
	if r.Indeterminate {
		issues = append(issues, Issue{Err: ErrIndeterminate})
	}
	return IssuesError(issues)
}

type PartitionController interface {
	EnableVFs(context.Context, []string, uint32, CommandOptions) (CommandResult, error)
	DisableVFs(context.Context, []string, CommandOptions) (CommandResult, error)
}

type BindingRequest struct {
	PCIAddress string
	Driver     string
	// ExpectedDriver, when supplied, prevents a stale plan from replacing
	// a different binding. The check is not an atomic kernel compare-and-swap.
	ExpectedDriver *string
	// AllowAutoprobeAfterUnbind clears the binding override without probing.
	// Upgrade managers use this before loading a replacement driver. The
	// default inhibits autoprobe until an explicit Bind/Reprobe operation.
	AllowAutoprobeAfterUnbind bool
}
type Binder interface {
	Bind(context.Context, BindingRequest) error
	Unbind(context.Context, BindingRequest) error
}

// ValidTargets prevents accidentally broadening an empty command to all devices.
func ValidTargets(names []string) error {
	if len(names) == 0 {
		return &Error{Kind: InvalidArgument, Message: "explicit targets required"}
	}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.TrimSpace(n) != n || strings.ContainsAny(n, "/\\\x00\r\n") || seen[n] {
			return &Error{Kind: InvalidArgument, Target: n, Message: "empty, invalid or duplicate target"}
		}
		seen[n] = true
	}
	return nil
}

func Pointer[T any](v T) *T { return &v }

// FieldError creates a normalized malformed-field error without inventing a
// valid zero value. It is useful to transport adapters as well as local readers.
func FieldError(target, field string, value any) error {
	return &Error{Kind: InvalidData, Op: field, Target: target, Message: fmt.Sprint(value)}
}
