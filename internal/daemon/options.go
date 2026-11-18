package daemon

// Stable resource identity shared across exporter restarts. Its metric set is
// validated and provisioned through the library, not encoded in this component.
const DefaultMetricGroupID = "9491461b-5a6d-4df2-9e44-5a91e58eb8ef"

type Options struct {
	MetricGroupID          string
	CreateMetricGroup      bool
	PCIDevicesPath         string
	TLSCA, TLSCert, TLSKey string
}

func DefaultOptions() Options {
	return Options{MetricGroupID: DefaultMetricGroupID, CreateMetricGroup: true}
}
