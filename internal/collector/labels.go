/*
Copyright 2025-2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package collector

import (
	"slices"
	"strconv"

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

	state = "state"

	smcVersion = "smc_version"
	pciBusID   = "pci_bus_id"
	numaNode   = "numa_node"
	rsdGroup   = "rsd_group"
	cpuList    = "cpu_list"
	isVF       = "is_vf"
	parentName = "parent_name"
	numVFs     = "num_vfs"
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

var deviceInfoOnlyLabels = []string{
	smcVersion,
	pciBusID,
	numaNode,
	rsdGroup,
	cpuList,
	isVF,
	parentName,
	numVFs,
}

func deviceInfoLabelNames(includePodLabels bool) []string {
	return append(slices.Clone(labelNames(includePodLabels)), deviceInfoOnlyLabels...)
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

func buildDeviceInfoLabels(device daemon.DeviceInfo, nodeName string, podResourceInfo map[DeviceName]PodResourceInfo, includePodLabels bool) prometheus.Labels {
	labels := buildLabels(device, nodeName, podResourceInfo, includePodLabels)

	labels[smcVersion] = device.SMCVersion
	labels[pciBusID] = device.PCIBusID
	labels[isVF] = strconv.FormatBool(device.IsVF)
	labels[parentName] = device.ParentName
	labels[numVFs] = strconv.FormatUint(uint64(device.NumVFs), 10)

	labels[numaNode] = ""
	labels[rsdGroup] = ""
	labels[cpuList] = ""
	if topology := device.Topology; topology != nil {
		labels[numaNode] = strconv.Itoa(int(topology.NUMANode))
		labels[rsdGroup] = topology.RSDGroup
		labels[cpuList] = topology.CPUList
	}

	return labels
}
