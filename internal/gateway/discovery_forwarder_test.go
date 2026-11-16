package gateway

import (
	"context"
	pb "github.com/OraCache/oracache/gen/gateway/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
	"time"
)

func TestDiscoveryForwarderRejectsStaleHintAndPreference(t *testing.T) {
	r, _ := NewRouter("a")
	if err := r.ApplyDiscovery(discoveryView(10000, "n1", 1), 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	f := &Forwarder{Router: r, preferred: map[uint32]string{0: "foreign:9001"}, MaxAttempts: 3, Backoff: time.Millisecond}
	var calls []string
	f.Dial = func(addr string) (pb.CacheGatewayClient, error) { calls = append(calls, addr); return nil, nil }
	err := f.forward(context.Background(), 0, true, func(_ context.Context, _ pb.CacheGatewayClient, addr string) error {
		if addr == "n1:9001" {
			st, _ := status.New(codes.FailedPrecondition, "NOT_LEADER").WithDetails(&errdetails.ErrorInfo{Reason: "NOT_LEADER", Metadata: map[string]string{"address": "foreign:9001"}})
			return st.Err()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range calls {
		if addr == "foreign:9001" {
			t.Fatal("bypassed catalog")
		}
	}
	// A retry cannot use a lease that expires during the previous attempt.
	calls = nil
	f.preferred = nil
	err = f.forward(context.Background(), 0, true, func(_ context.Context, _ pb.CacheGatewayClient, _ string) error {
		if e := r.ApplyDiscovery(discoveryView(1, "n1", 1), 2, time.Now().Add(-time.Second)); e != nil {
			t.Fatal(e)
		}
		return status.Error(codes.Unavailable, "asymmetric connection failure")
	})
	if err == nil || len(calls) != 1 {
		t.Fatalf("expiry bypassed: %v %v", calls, err)
	}
}
