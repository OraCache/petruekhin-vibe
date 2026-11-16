package node_test

import (
	"context"
	"fmt"
	cp "github.com/OraCache/oracache/gen/controlplane/v1"
	pb "github.com/OraCache/oracache/gen/gateway/v1"
	"github.com/OraCache/oracache/internal/cpapi"
	"github.com/OraCache/oracache/internal/discovery"
	"github.com/OraCache/oracache/internal/gateway"
	"github.com/OraCache/oracache/internal/node"
	"github.com/OraCache/oracache/internal/statestore"
	"github.com/OraCache/oracache/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// Real Raft HTTP + client/CP/gateway gRPC, no configured storage list in CP or
// gateway. A CP transport outage keeps the same in-memory catalog/epoch alive.
func TestDiscoveryClusterFailoverAndCPLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog, err := discovery.New("a", "g", 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var blocked atomic.Bool
	cpLis := listen(t)
	cpSrv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if blocked.Load() {
			return nil, status.Error(codes.Unavailable, "test CP transport outage")
		}
		return handler(ctx, req)
	}))
	cp.RegisterControlPlaneAPIServer(cpSrv, cpapi.New(statestore.NewMemory(), cpapi.WithDiscovery(catalog), cpapi.WithLogger(quiet), cpapi.WithHeartbeatInterval(time.Second)))
	go cpSrv.Serve(cpLis)
	defer cpSrv.Stop()
	conn, err := grpc.NewClient(cpLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	router, _ := gateway.NewRouter("a")
	poller := gateway.NewPoller(cp.NewControlPlaneAPIClient(conn), router, "gw")
	poller.Logger = quiet
	// An empty snapshot must bootstrap successfully, but cannot route yet.
	if err := poller.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Replicas(0, true); err == nil {
		t.Fatal("empty catalog unexpectedly routes")
	}

	gl := []net.Listener{listen(t), listen(t), listen(t)}
	rl := []net.Listener{listen(t), listen(t), listen(t)}
	var peers []node.Peer
	for i := range gl {
		peers = append(peers, node.Peer{ID: uint64(i + 1), NodeID: fmt.Sprintf("n%d", i), ClientAddr: gl[i].Addr().String(), RaftURL: "http://" + rl[i].Addr().String() + "/raft"})
	}
	p := &poc{}
	for i := range peers {
		n, err := node.New(node.Config{NodeID: peers[i].NodeID, Region: "a", GroupID: "g", PeerID: peers[i].ID, Peers: peers, GRPCListener: gl[i], RaftListener: rl[i], CPAddr: cpLis.Addr().String(), HeartbeatInterval: time.Second, ConfigPullInterval: time.Second, Store: store.Config{MaxVersions: 3}, TickInterval: 20 * time.Millisecond, ElectionTicks: 10, Logger: quiet})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- n.Run(ctx); close(done) }()
		t.Cleanup(func() { n.Close(); <-done })
		p.nodes = append(p.nodes, n)
		p.runDone = append(p.runDone, done)
	}
	fwd := &gateway.Forwarder{Router: router, Logger: quiet, Timeout: 2 * time.Second}
	defer fwd.Close()
	gwSrv := grpc.NewServer()
	pb.RegisterCacheGatewayServer(gwSrv, fwd)
	gwLis := listen(t)
	go gwSrv.Serve(gwLis)
	defer gwSrv.Stop()
	gwConn, err := grpc.NewClient(gwLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer gwConn.Close()
	client := pb.NewCacheGatewayClient(gwConn)
	leader := p.leaderIndex(t, -1)
	eventually(t, "three eligible registered nodes", func() bool {
		if poller.Refresh(ctx) != nil {
			return false
		}
		replicas, err := router.Replicas(0, true)
		return err == nil && len(replicas) == 3
	})
	put, err := client.Put(ctx, &pb.PutRequest{Key: "discovery", Data: []byte("confirmed")})
	if err != nil || put.Status != pb.PutStatus_PUT_STATUS_OK {
		t.Fatalf("Put %v %v", put, err)
	}

	blocked.Store(true)
	if err := poller.Refresh(ctx); err == nil {
		t.Fatal("CP outage not exercised")
	}
	// Still works inside the last received lease (no CP on the data path).
	got, err := client.Get(ctx, &pb.GetRequest{Key: "discovery"})
	if err != nil || string(got.GetData()) != "confirmed" {
		t.Fatalf("remaining lease %v %v", got, err)
	}
	eventually(t, "local leases expire without CP refresh", func() bool { _, err := router.Replicas(0, true); return err != nil })
	_, err = client.Get(ctx, &pb.GetRequest{Key: "discovery"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expired request must fail closed: %v", err)
	}
	blocked.Store(false)
	eventually(t, "heartbeat and routing recover", func() bool {
		if poller.Refresh(ctx) != nil {
			return false
		}
		got, err := client.Get(ctx, &pb.GetRequest{Key: "discovery"})
		return err == nil && string(got.GetData()) == "confirmed"
	})
	p.nodes[leader].Close()
	<-p.runDone[leader]
	next := p.leaderIndex(t, leader)
	if next == leader {
		t.Fatal("leader did not change")
	}
	eventually(t, "dead leader removed by TTL", func() bool {
		if poller.Refresh(ctx) != nil {
			return false
		}
		rs, err := router.Replicas(0, true)
		if err != nil || len(rs) != 2 {
			return false
		}
		for _, r := range rs {
			if r.NodeID == peers[leader].NodeID {
				return false
			}
		}
		return true
	})
	got, err = client.Get(ctx, &pb.GetRequest{Key: "discovery"})
	if err != nil || string(got.GetData()) != "confirmed" {
		t.Fatalf("failover Get %v %v", got, err)
	}
	updated, err := client.Put(ctx, &pb.PutRequest{Key: "discovery", Data: []byte("after"), ExpectedRev: &put.Rev})
	if err != nil || updated.GetStatus() != pb.PutStatus_PUT_STATUS_OK || updated.GetRev() <= put.Rev {
		t.Fatalf("failover Put %v %v", updated, err)
	}
}
