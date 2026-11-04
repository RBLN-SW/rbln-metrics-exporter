// Package smdtest provides in-process SMD APIs for exporter integration tests.
package smdtest

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	legacy "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

type Server struct {
	pb.UnimplementedInventoryServiceServer
	pb.UnimplementedMetricServiceServer
	pb.UnimplementedTopologyServiceServer
	Mu                  sync.Mutex
	Devices             []*pb.DeviceInfo
	Samples             []*pb.MetricSample
	Entries             []*pb.TopologyEntry
	Groups              map[string]*pb.MetricGroup
	Created             []*pb.CreateMetricGroupRequest
	InventoryRequests   []*pb.ListDevicesRequest
	SampleCalls         int
	VersionCalls        int
	MemoryCalls         int
	PageSize            int
	Errors              map[string]error
	Unreachable         []*pb.EntityStatus
	TopologyUnreachable []*pb.EntityStatus
	Legacy              *LegacyServer
	LegacyOnly          bool
}

func (s *Server) Set(devices []*pb.DeviceInfo, samples []*pb.MetricSample) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Devices = proto.Clone(&pb.ListDevicesResponse{Items: devices}).(*pb.ListDevicesResponse).Items
	s.Samples = proto.Clone(&pb.SampleMetricsResponse{Samples: samples}).(*pb.SampleMetricsResponse).Samples
}

func Serve(t testing.TB, s *Server, address string, transport *TLS) (string, func()) {
	t.Helper()
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("test SMD must listen on a loopback IP: %q", address)
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(transport.ServerConfig.Clone())))
	if !s.LegacyOnly {
		pb.RegisterInventoryServiceServer(g, s)
		pb.RegisterMetricServiceServer(g, s)
		pb.RegisterTopologyServiceServer(g, s)
	}
	if s.Legacy != nil {
		legacy.RegisterRBLNServicesServer(g, s.Legacy)
	}
	go func() { _ = g.Serve(l) }()
	return l.Addr().String(), g.Stop
}

func page[T any](items []T, token string, limit int) ([]T, string) {
	start, _ := strconv.Atoi(token)
	if limit <= 0 {
		limit = 1000
	}
	end := min(start+limit, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[start:end], next
}

func (s *Server) ListDevices(_ context.Context, r *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.InventoryRequests = append(s.InventoryRequests, proto.Clone(r).(*pb.ListDevicesRequest))
	items, next := page(s.Devices, r.GetPageToken(), s.PageSize)
	return proto.Clone(&pb.ListDevicesResponse{Items: items, NextPageToken: next}).(*pb.ListDevicesResponse), s.Errors["inventory"]
}

func (s *Server) ListTopology(_ context.Context, r *pb.ListTopologyRequest) (*pb.ListTopologyResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	entries, next := page(s.Entries, r.GetPageToken(), s.PageSize)
	return proto.Clone(&pb.ListTopologyResponse{Entries: entries, NextPageToken: next, Unreachable: s.TopologyUnreachable}).(*pb.ListTopologyResponse), s.Errors["topology"]
}

func (s *Server) GetVersion(context.Context, *pb.GetVersionRequest) (*pb.GetVersionResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.VersionCalls++
	return &pb.GetVersionResponse{FwVersion: "test-firmware", DrvVersion: "test-driver", SmcVersion: "test-smc"}, s.Errors["version"]
}

func (s *Server) GetMemorySpec(context.Context, *pb.GetMemorySpecRequest) (*pb.GetMemorySpecResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.MemoryCalls++
	return &pb.GetMemorySpecResponse{Memory: &pb.MemorySpec{TotalBytes: 35 << 30}}, s.Errors["memory"]
}

func (s *Server) ListMetricGroups(context.Context, *pb.ListMetricGroupsRequest) (*pb.ListMetricGroupsResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	var groups []*pb.MetricGroup
	for _, g := range s.Groups {
		groups = append(groups, proto.Clone(g).(*pb.MetricGroup))
	}
	return &pb.ListMetricGroupsResponse{Items: groups}, s.Errors["groups"]
}

func (s *Server) CreateMetricGroup(_ context.Context, r *pb.CreateMetricGroupRequest) (*pb.CreateMetricGroupResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if err := s.Errors["create"]; err != nil {
		return nil, err
	}
	if s.Groups == nil {
		s.Groups = map[string]*pb.MetricGroup{}
	}
	if s.Groups[r.GetIdempotencyKey()] != nil {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	s.Created = append(s.Created, proto.Clone(r).(*pb.CreateMetricGroupRequest))
	g := &pb.MetricGroup{Id: r.GetIdempotencyKey(), Name: r.GetName(), MetricIds: r.GetMetricIds(), DeviceScope: r.GetDeviceScope(), SamplingPolicy: r.GetSamplingPolicy()}
	s.Groups[g.Id] = proto.Clone(g).(*pb.MetricGroup)
	return &pb.CreateMetricGroupResponse{Group: g}, nil
}

func (s *Server) SampleMetrics(_ context.Context, r *pb.SampleMetricsRequest) (*pb.SampleMetricsResponse, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.SampleCalls++
	if s.Groups[r.GetGroup().GetId()] == nil {
		return nil, status.Error(codes.NotFound, "no group")
	}
	return proto.Clone(&pb.SampleMetricsResponse{Samples: s.Samples, Unreachable: s.Unreachable}).(*pb.SampleMetricsResponse), s.Errors["samples"]
}

func Device(name string, vf bool) *pb.DeviceInfo {
	n := uint32(0)
	return &pb.DeviceInfo{Name: name, Uuid: "shared-uuid", PciBusId: "0000:17:00.0", IsVf: vf, NpuId: &n, State: pb.DeviceState_DEVICE_STATE_READY, Versions: &pb.VersionInfo{DrvVersion: "inline-driver", FwVersion: "inline-firmware"}, Memory: &pb.MemorySpec{TotalBytes: 35 << 30}}
}

func Float(name string, id pb.MetricId, value float64) *pb.MetricSample {
	return &pb.MetricSample{Device: &pb.DeviceRef{Name: name}, MetricId: id, TypedValue: &pb.MetricSample_FloatValue{FloatValue: value}}
}

func Int(name string, id pb.MetricId, value int64) *pb.MetricSample {
	return &pb.MetricSample{Device: &pb.DeviceRef{Name: name}, MetricId: id, TypedValue: &pb.MetricSample_IntValue{IntValue: value}}
}
