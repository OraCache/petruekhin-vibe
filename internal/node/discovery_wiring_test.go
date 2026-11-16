package node_test

import (
	"context"
	pb "github.com/OraCache/oracache/gen/controlplane/v1"
	"github.com/OraCache/oracache/internal/cpapi"
	"github.com/OraCache/oracache/internal/discovery"
	"github.com/OraCache/oracache/internal/node"
	"github.com/OraCache/oracache/internal/statestore"
	"github.com/OraCache/oracache/internal/store"
	"google.golang.org/grpc"
	"testing"
	"time"
)

func TestNodeAdvertisesConfiguredClientAddress(t *testing.T) {
	cp, gl, rl := listen(t), listen(t), listen(t)
	catalog, err := discovery.New("a", "g", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pb.RegisterControlPlaneAPIServer(srv, cpapi.New(statestore.NewMemory(), cpapi.WithDiscovery(catalog), cpapi.WithHeartbeatInterval(time.Second)))
	go srv.Serve(cp)
	defer srv.Stop()
	// Bind and advertised addresses intentionally differ.
	advertised := "storage.example:51000"
	n, err := node.New(node.Config{NodeID: "n", Region: "a", GroupID: "g", PeerID: 1, Peers: []node.Peer{{ID: 1, NodeID: "n", ClientAddr: advertised, RaftURL: "http://" + rl.Addr().String() + "/raft"}}, GRPCListener: gl, RaftListener: rl, CPAddr: cp.Addr().String(), Store: store.Config{MaxVersions: 1}, TickInterval: 20 * time.Millisecond, ElectionTicks: 5})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	defer func() { cancel(); n.Close(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := catalog.Snapshot(time.Now())
		if len(snapshot.Observations) > 0 {
			h := snapshot.Observations[0].Heartbeat
			if h.ClientAddress != advertised || h.Partitions[0].GroupId != "g" {
				t.Fatalf("heartbeat: %v", h)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("node did not register a ready discovery observation")
}
