package collector

const (
	card            = "card"
	name            = "name"
	uuid            = "uuid"
	deviceID        = "deviceID"
	hostname        = "hostname"
	namespace       = "namespace"
	pod             = "pod"
	container       = "container"
	driverVersion   = "driver_version"
	firmwareVersion = "firmware_version"
)

var commonLabels = []string{
	card,
	name,
	uuid,
	deviceID,
	hostname,
	namespace,
	pod,
	container,
	driverVersion,
	firmwareVersion,
}
