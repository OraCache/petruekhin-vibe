package gateway

import (
	"context"
	cp "github.com/OraCache/oracache/gen/controlplane/v1"
	pb "github.com/OraCache/oracache/gen/gateway/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
	"time"
)

func discoveryView(remaining uint64, leader string, term uint64) *cp.DiscoverySnapshot {
	s := &cp.DiscoverySnapshot{Region: "a", GroupId: "g"}
	for _, id := range []string{"n1", "n2"} {
		role := cp.RaftRole_RAFT_ROLE_FOLLOWER
		if id == leader {
			role = cp.RaftRole_RAFT_ROLE_LEADER
		}
		s.Observations = append(s.Observations, &cp.DiscoveryObservation{RemainingMs: remaining, Heartbeat: &cp.HeartbeatRequest{NodeId: id, Region: "a", ClientAddress: id + ":9001", Sequence: 1, Partitions: []*cp.PartitionStatus{{GroupId: "g", Role: role, Term: term, LeaderId: leader, Ready: true}}}})
	}
	return s
}
func TestDiscoveryLeaseLocalExpiryAndOrdering(t *testing.T) {
	r, _ := NewRouter("a")
	now := time.Now()
	if err := r.ApplyDiscovery(discoveryView(1000, "n1", 1), 1, now); err != nil {
		t.Fatal(err)
	}
	l, err := r.WriteLeader(0)
	if err != nil || l.NodeID != "n1" {
		t.Fatalf("%v %v", l, err)
	}
	if err := r.ApplyDiscovery(discoveryView(10000, "n2", 2), 1, now); err == nil {
		t.Fatal("reordered revision accepted")
	}
	if err := r.ApplyDiscovery(discoveryView(1000, "n2", 2), 2, now); err != nil {
		t.Fatal(err)
	}
	l, _ = r.GetLeader(0)
	if l.NodeID != "n2" {
		t.Fatal(l)
	}
	// Charge the complete fetch duration: an already consumed lease must fail closed.
	if err := r.ApplyDiscovery(discoveryView(1000, "n2", 2), 3, now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetLeader(0); err == nil {
		t.Fatal("expired leader")
	}
	if rs, err := r.Replicas(0, true); err == nil || len(rs) != 0 {
		t.Fatalf("expired replicas %v %v", rs, err)
	}
	if err := r.ApplyDiscovery(discoveryView(1000, "n2", 2), 4, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetLeader(0); err != nil {
		t.Fatal(err)
	}
}
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
