// Package system defines version 1 of the system software contract shared by
// Rebellions infrastructure components. It has no Kubernetes, protobuf or CGO
// dependency. Implementations live in pkg/host/v1, pkg/smi/v1 and pkg/smd/v1.
//
// PCI addresses locate functions on one host at observation time; names, device
// nodes and enumeration indexes are not persistent identities. Never construct
// a device path or infer PF/VF relationships from Name or Index. A nil optional
// field is unavailable, not zero. Unknown lifecycle values remain StateUnknown.
//
// Queries may return partial snapshots with Issues. Consumers decide whether
// partial observations are sufficient for their policy. Commands never retry
// automatically and never fall back to another transport after submission.
// An indeterminate command must be reconciled against fresh observations.
//
// The meaning and units of existing fields and error kinds are part of the v1
// contract. Incompatible changes require a new API package version. Add small
// interfaces for new operations rather than extending existing interfaces that
// consumers may implement. The original device/rblnsmi/rsdgroup APIs remain
// separate compatibility surfaces.
package system
