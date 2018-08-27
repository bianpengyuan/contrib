// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// nolint:lll
//go:generate go run $GOPATH/src/istio.io/contrib/vendor/istio.io/istio/mixer/tools/mixgen/main.go adapter -n stackdriver -s=false  -c $GOPATH/src/istio.io/contrib/adapters/stackdriver/config/config.proto_descriptor -t metric -t logentry -t tracespan -t edge -o $GOPATH/src/istio.io/contrib/adapters/stackdriver/operatorconfig/stackdriver-nosession.yaml

package server

import (
	"context"
	"fmt"
	"net"
	"sync"

	proto "github.com/gogo/protobuf/proto"
	types "github.com/gogo/protobuf/types"
	"google.golang.org/grpc"
	adptModel "istio.io/api/mixer/adapter/model/v1beta1"
	v1beta1 "istio.io/api/policy/v1beta1"
	"istio.io/contrib/adapters/stackdriver"
	"istio.io/contrib/adapters/stackdriver/config"
	"istio.io/istio/mixer/pkg/adapter"
	"istio.io/istio/mixer/pkg/pool"
	"istio.io/istio/mixer/pkg/runtime/handler"
	"istio.io/istio/mixer/template/edge"
	"istio.io/istio/mixer/template/logentry"
	"istio.io/istio/mixer/template/metric"
	"istio.io/istio/mixer/template/tracespan"
)

type (
	// Server is basic server interface
	Server interface {
		Addr() string
		Close() error
		PromPort() int
		Run()
	}

	// NoSessionServer models no session adapter backend.
	NoSessionServer struct {
		listener net.Listener
		shutdown chan error
		server   *grpc.Server

		rawcfg      []byte
		builder     adapter.HandlerBuilder
		env         adapter.Env
		builderLock sync.RWMutex

		metricHandler    metric.Handler
		logEntryHandler  logentry.Handler
		traceSpanHandler tracespan.Handler
		edgeHandler      edge.Handler
	}
)

var _ metric.HandleMetricServiceServer = &NoSessionServer{}
var _ logentry.HandleLogEntryServiceServer = &NoSessionServer{}
var _ tracespan.HandleTraceSpanServiceServer = &NoSessionServer{}
var _ edge.HandleEdgeServiceServer = &NoSessionServer{}

func (s *NoSessionServer) updateHandlers(rawcfg []byte) error {
	cfg := &config.Params{}

	if err := cfg.Unmarshal(rawcfg); err != nil {
		return err
	}

	s.builderLock.Lock()
	defer s.builderLock.Unlock()
	if configEqual(rawcfg, s.rawcfg) {
		return nil
	}

	s.env.Logger().Infof("Loaded handler with: %v", cfg)

	s.builder.SetAdapterConfig(cfg)
	if ce := s.builder.Validate(); ce != nil {
		return ce
	}

	h, err := s.builder.Build(context.Background(), s.env)
	if err != nil {
		s.env.Logger().Errorf("could not build: %v", err)
		return err
	}
	s.rawcfg = rawcfg
	s.metricHandler = h.(metric.Handler)
	s.logEntryHandler = h.(logentry.Handler)
	s.traceSpanHandler = h.(tracespan.Handler)
	s.edgeHandler = h.(edge.Handler)
	return nil
}

func configEqual(orig []byte, new []byte) bool {
	oc := &config.Params{}
	nc := &config.Params{}
	oc.Unmarshal(orig)
	nc.Unmarshal(new)

	return proto.Equal(oc, nc)
}

func (s *NoSessionServer) getMetricHandler(rawcfg []byte) (metric.Handler, error) {
	s.builderLock.RLock()
	if configEqual(rawcfg, s.rawcfg) {
		h := s.metricHandler
		s.builderLock.RUnlock()
		return h, nil
	}
	s.builderLock.RUnlock()
	if err := s.updateHandlers(rawcfg); err != nil {
		return nil, err
	}

	// establish session
	return s.metricHandler, nil
}

func (s *NoSessionServer) getLogEntryHandler(rawcfg []byte) (logentry.Handler, error) {
	s.builderLock.RLock()
	if configEqual(rawcfg, s.rawcfg) {
		h := s.logEntryHandler
		s.builderLock.RUnlock()
		return h, nil
	}
	s.builderLock.RUnlock()
	if err := s.updateHandlers(rawcfg); err != nil {
		return nil, err
	}

	// establish session
	return s.logEntryHandler, nil
}

func (s *NoSessionServer) getTraceSpanHandler(rawcfg []byte) (tracespan.Handler, error) {
	s.builderLock.RLock()
	if configEqual(rawcfg, s.rawcfg) {
		h := s.traceSpanHandler
		s.builderLock.RUnlock()
		return h, nil
	}
	s.builderLock.RUnlock()
	if err := s.updateHandlers(rawcfg); err != nil {
		return nil, err
	}

	// establish session
	return s.traceSpanHandler, nil
}

func (s *NoSessionServer) getEdgeHandler(rawcfg []byte) (edge.Handler, error) {
	s.builderLock.RLock()
	if configEqual(rawcfg, s.rawcfg) {
		h := s.edgeHandler
		s.builderLock.RUnlock()
		return h, nil
	}
	s.builderLock.RUnlock()
	if err := s.updateHandlers(rawcfg); err != nil {
		return nil, err
	}

	// establish session
	return s.edgeHandler, nil
}

func metricInstances(in []*metric.InstanceMsg) []*metric.Instance {
	out := make([]*metric.Instance, 0, len(in))
	for _, inst := range in {
		out = append(out, &metric.Instance{
			Name:                        inst.Name,
			Value:                       decodeValue(inst.Value.GetValue()),
			Dimensions:                  decodeDimensions(inst.Dimensions),
			MonitoredResourceType:       inst.MonitoredResourceType,
			MonitoredResourceDimensions: decodeDimensions(inst.MonitoredResourceDimensions),
		})
	}
	return out
}

func logEntryInstances(in []*logentry.InstanceMsg) []*logentry.Instance {
	out := make([]*logentry.Instance, 0, len(in))
	for _, inst := range in {
		t, err := types.TimestampFromProto(inst.Timestamp.GetValue())
		if err != nil {
			continue
		}
		out = append(out, &logentry.Instance{
			Name:                        inst.Name,
			Variables:                   decodeDimensions(inst.Variables),
			Timestamp:                   t,
			Severity:                    inst.Severity,
			MonitoredResourceType:       inst.MonitoredResourceType,
			MonitoredResourceDimensions: decodeDimensions(inst.MonitoredResourceDimensions),
		})
	}
	return out
}

func traceSpanInstances(in []*tracespan.InstanceMsg) []*tracespan.Instance {
	out := make([]*tracespan.Instance, 0, len(in))
	for _, inst := range in {
		st, err := types.TimestampFromProto(inst.StartTime.GetValue())
		if err != nil {
			continue
		}
		et, err := types.TimestampFromProto(inst.EndTime.GetValue())
		if err != nil {
			continue
		}
		out = append(out, &tracespan.Instance{
			Name:                inst.Name,
			TraceId:             inst.TraceId,
			SpanId:              inst.SpanId,
			ParentSpanId:        inst.ParentSpanId,
			SpanName:            inst.SpanName,
			StartTime:           st,
			EndTime:             et,
			SpanTags:            decodeDimensions(inst.SpanTags),
			HttpStatusCode:      inst.HttpStatusCode,
			ClientSpan:          inst.ClientSpan,
			RewriteClientSpanId: inst.RewriteClientSpanId,
		})
	}
	return out
}

func edgeInstances(in []*edge.InstanceMsg) []*edge.Instance {
	out := make([]*edge.Instance, 0, len(in))
	for _, inst := range in {
		t, err := types.TimestampFromProto(inst.Timestamp.GetValue())
		if err != nil {
			continue
		}
		out = append(out, &edge.Instance{
			Name:                         inst.Name,
			Timestamp:                    t,
			SourceWorkloadName:           inst.SourceWorkloadName,
			SourceWorkloadNamespace:      inst.SourceWorkloadNamespace,
			SourceOwner:                  inst.SourceOwner,
			SourceUid:                    inst.SourceUid,
			DestinationWorkloadNamespace: inst.DestinationWorkloadNamespace,
			DestinationWorkloadName:      inst.DestinationWorkloadName,
			DestinationOwner:             inst.DestinationOwner,
			DestinationUid:               inst.DestinationUid,
			ContextProtocol:              inst.ContextProtocol,
			ApiProtocol:                  inst.ApiProtocol,
		})
	}
	return out
}

func decodeDimensions(in map[string]*v1beta1.Value) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = decodeValue(v.GetValue())
	}
	return out
}

func decodeValue(in interface{}) interface{} {
	switch t := in.(type) {
	case *v1beta1.Value_StringValue:
		return t.StringValue
	case *v1beta1.Value_Int64Value:
		return t.Int64Value
	case *v1beta1.Value_DoubleValue:
		return t.DoubleValue
	case *v1beta1.Value_DurationValue:
		if d, e := types.DurationFromProto(t.DurationValue.GetValue()); e == nil {
			return d
		}
		return fmt.Sprintf("%v", in)
	case *v1beta1.Value_IpAddressValue:
		return t.IpAddressValue.GetValue()
	default:
		return fmt.Sprintf("%v", in)
	}
}

// HandleMetric records metric entries and responds with the programmed response
func (s *NoSessionServer) HandleMetric(ctx context.Context, r *metric.HandleMetricRequest) (*adptModel.ReportResult, error) {
	h, err := s.getMetricHandler(r.AdapterConfig.Value)
	if err != nil {
		return nil, err
	}

	if err = h.HandleMetric(ctx, metricInstances(r.Instances)); err != nil {
		s.env.Logger().Errorf("Could not process: %v", err)
		return nil, err
	}

	return &adptModel.ReportResult{}, nil
}

// HandleLogEntry records logentry entries and responds with the programmed response
func (s *NoSessionServer) HandleLogEntry(ctx context.Context, r *logentry.HandleLogEntryRequest) (*adptModel.ReportResult, error) {
	h, err := s.getLogEntryHandler(r.AdapterConfig.Value)
	if err != nil {
		return nil, err
	}

	if err = h.HandleLogEntry(ctx, logEntryInstances(r.Instances)); err != nil {
		s.env.Logger().Errorf("Could not process: %v", err)
		return nil, err
	}

	return &adptModel.ReportResult{}, nil
}

// HandleTraceSpan records tracespan entries and responds with the programmed response
func (s *NoSessionServer) HandleTraceSpan(ctx context.Context, r *tracespan.HandleTraceSpanRequest) (*adptModel.ReportResult, error) {
	h, err := s.getTraceSpanHandler(r.AdapterConfig.Value)
	if err != nil {
		return nil, err
	}

	if err = h.HandleTraceSpan(ctx, traceSpanInstances(r.Instances)); err != nil {
		s.env.Logger().Errorf("Could not process: %v", err)
		return nil, err
	}

	return &adptModel.ReportResult{}, nil
}

// HandleEdge records edge entries and responds with the programmed response
func (s *NoSessionServer) HandleEdge(ctx context.Context, r *edge.HandleEdgeRequest) (*adptModel.ReportResult, error) {
	h, err := s.getEdgeHandler(r.AdapterConfig.Value)
	if err != nil {
		return nil, err
	}

	if err = h.HandleEdge(ctx, edgeInstances(r.Instances)); err != nil {
		s.env.Logger().Errorf("Could not process: %v", err)
		return nil, err
	}

	return &adptModel.ReportResult{}, nil
}

// Addr returns the listening address of the server
func (s *NoSessionServer) Addr() string {
	return s.listener.Addr().String()
}

// Run starts the server run
func (s *NoSessionServer) Run() {
	s.shutdown = make(chan error, 1)
	go func() {
		err := s.server.Serve(s.listener)

		// notify closer we're done
		s.shutdown <- err
	}()
}

// Wait waits for server to stop
func (s *NoSessionServer) Wait() error {
	if s.shutdown == nil {
		return fmt.Errorf("server not running")
	}

	err := <-s.shutdown
	s.shutdown = nil
	return err
}

// Close gracefully shuts down the server
func (s *NoSessionServer) Close() error {
	if s.shutdown != nil {
		s.server.GracefulStop()
		_ = s.Wait()
	}

	if s.listener != nil {
		_ = s.listener.Close()
	}

	return nil
}

// NewStackdriverServer creates a new no session server from given args.
func NewStackdriverServer(addr uint16) (*NoSessionServer, error) {
	saddr := fmt.Sprintf(":%d", addr)

	gp := pool.NewGoroutinePool(5, false)
	inf := stackdriver.GetInfo()
	s := &NoSessionServer{
		builder: inf.NewBuilder(),
		env:     handler.NewEnv(0, "stackdriver-nosession", gp),
		rawcfg:  []byte{0xff, 0xff},
	}
	var err error
	if s.listener, err = net.Listen("tcp", saddr); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("unable to listen on socket: %v", err)
	}

	fmt.Printf("listening on :%v\n", s.listener.Addr())
	s.server = grpc.NewServer()
	metric.RegisterHandleMetricServiceServer(s.server, s)
	logentry.RegisterHandleLogEntryServiceServer(s.server, s)
	tracespan.RegisterHandleTraceSpanServiceServer(s.server, s)
	edge.RegisterHandleEdgeServiceServer(s.server, s)
	return s, nil
}
