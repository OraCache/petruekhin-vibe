package tracing

import (
	"context"
	"log/slog"
	"net"
	"testing"

	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type traceSink struct {
	collector.UnimplementedTraceServiceServer
	requests chan *collector.ExportTraceServiceRequest
}

func (s traceSink) Export(ctx context.Context, req *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, error) {
	select {
	case s.requests <- req:
		return &collector.ExportTraceServiceResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestStartExportsOnlySampledSpansOnShutdown(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	tp, shutdown, err := Start("storage-node", "a-1")
	if err != nil || tp != nil {
		t.Fatalf("tracing without an endpoint: %v, %v", tp, err)
	}
	shutdown(slog.Default())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := grpc.NewServer()
	defer server.Stop()
	sink := traceSink{requests: make(chan *collector.ExportTraceServiceRequest, 1)}
	collector.RegisterTraceServiceServer(server, sink)
	go server.Serve(listener)

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+listener.Addr().String())
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "true")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	tp, shutdown, err = Start("storage-node", "a-1")
	if err != nil {
		t.Fatal(err)
	}
	tracer := tp.Tracer("test")
	_, root := tracer.Start(t.Context(), "unsampled root")
	root.End()
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	_, child := tracer.Start(trace.ContextWithRemoteSpanContext(t.Context(), parent), "sampled child")
	child.End()
	shutdown(slog.Default())

	select {
	case req := <-sink.requests:
		var names []string
		attrs := map[string]string{}
		for _, res := range req.GetResourceSpans() {
			for _, attr := range res.GetResource().GetAttributes() {
				attrs[attr.Key] = attr.GetValue().GetStringValue()
			}
			for _, scope := range res.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					names = append(names, span.Name)
				}
			}
		}
		if len(names) != 1 || names[0] != "sampled child" || attrs["service.name"] != "storage-node" || attrs["service.instance.id"] != "a-1" {
			t.Fatalf("exported spans %v with resource %v", names, attrs)
		}
	default:
		t.Fatal("no trace exported to loopback receiver on shutdown")
	}
}
