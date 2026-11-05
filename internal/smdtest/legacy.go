package smdtest

import (
	"context"
	"sync"

	legacy "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type LegacyServer struct {
	legacy.UnimplementedRBLNServicesServer
	Mu                  sync.Mutex
	Devices             []*legacy.Device
	Infos               []*legacy.DeviceInfo
	Entries             []*legacy.TopologyEntry
	Errors              map[string]error
	Calls               map[string]int
	TotalErrorAfterSend bool
}

func (s *LegacyServer) called(method string) {
	if s.Calls == nil {
		s.Calls = map[string]int{}
	}
	s.Calls[method]++
}

func (s *LegacyServer) GetDeviceList(_ *legacy.Empty, stream grpc.ServerStreamingServer[legacy.Device]) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.called("devices")
	if err := s.Errors["devices"]; err != nil {
		return err
	}
	for _, d := range s.Devices {
		if err := stream.Send(proto.Clone(d).(*legacy.Device)); err != nil {
			return err
		}
	}
	return nil
}

func (s *LegacyServer) GetTotalInfo(_ *legacy.Empty, stream grpc.ServerStreamingServer[legacy.DeviceInfo]) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.called("total")
	if err := s.Errors["total"]; err != nil && !s.TotalErrorAfterSend {
		return err
	}
	for _, d := range s.Infos {
		if err := stream.Send(proto.Clone(d).(*legacy.DeviceInfo)); err != nil {
			return err
		}
	}
	return s.Errors["total"]
}

func (s *LegacyServer) GetVersion(context.Context, *legacy.Device) (*legacy.VersionInfo, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.called("version")
	return &legacy.VersionInfo{ErrStatus: legacy.Status_SUCCEED, SmcVersion: "legacy-smc"}, s.Errors["version"]
}

func (s *LegacyServer) RblnListTopology(context.Context, *legacy.DeviceFilter) (*legacy.RblnListTopologyResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.called("topology")
	return proto.Clone(&legacy.RblnListTopologyResponse{Entries: s.Entries}).(*legacy.RblnListTopologyResponse), s.Errors["topology"]
}
