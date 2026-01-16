package collector

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

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

var baseLabels = []string{
	card,
	name,
	uuid,
	deviceID,
	hostname,
	driverVersion,
	firmwareVersion,
}

var commonLabels = append(slices.Clone(baseLabels), namespace, pod, container)

func labelNames(includePodLabels bool) []string {
	if includePodLabels {
		return commonLabels
	}
	return baseLabels
}

func buildLabels(device daemon.DeviceInfo, nodeName string, podResourceInfo map[DeviceName]PodResourceInfo, includePodLabels bool) prometheus.Labels {
	labels := prometheus.Labels{
		card:            device.Card,
		uuid:            device.UUID,
		name:            device.Name,
		deviceID:        device.DeviceID,
		hostname:        nodeName,
		driverVersion:   device.DriverVersion,
		firmwareVersion: device.FirmwareVersion,
	}

	if includePodLabels {
		info := podResourceInfo[DeviceName(device.Name)]
		labels[namespace] = info.Namespace
		labels[pod] = info.Name
		labels[container] = info.ContainerName
	}

	return labels
}
