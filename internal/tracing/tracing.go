package tracing

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// Start enables batched OTLP/gRPC export only when an endpoint is configured.
func Start(service, instance string) (trace.TracerProvider, func(*slog.Logger), error) {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" &&
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return nil, func(*slog.Logger) {}, nil
	}
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", service),
		attribute.String("service.instance.id", instance),
	), resource.WithFromEnv())
	if err != nil {
		return nil, nil, err
	}
	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exporter))
	return tp, func(logger *slog.Logger) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			logger.Warn("shut down tracing", "error", err)
		}
	}, nil
}

func ServerOption(tp trace.TracerProvider) grpc.ServerOption {
	if tp == nil {
		return grpc.EmptyServerOption{}
	}
	return grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithTracerProvider(tp),
		otelgrpc.WithPropagators(propagation.TraceContext{}), otelgrpc.WithMeterProvider(noop.NewMeterProvider())))
}

func ClientOption(tp trace.TracerProvider) grpc.DialOption {
	if tp == nil {
		return grpc.EmptyDialOption{}
	}
	return grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithTracerProvider(tp),
		otelgrpc.WithPropagators(propagation.TraceContext{}), otelgrpc.WithMeterProvider(noop.NewMeterProvider())))
}
