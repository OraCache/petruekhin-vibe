package node_test

import (
	"context"
	"fmt"
	cp "github.com/OraCache/oracache/gen/controlplane/v1"
	pb "github.com/OraCache/oracache/gen/gateway/v1"
	"github.com/OraCache/oracache/internal/cpapi"
	"github.com/OraCache/oracache/internal/cpconfig"
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

// Real Raft HTTP and TCP client/CP/gateway gRPC with static regional replicas.
func TestStaticLeaderHintsClusterFailoverAndCPLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	state := statestore.NewMemory()
	var blocked atomic.Bool
	cpLis := listen(t)
	cpSrv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if blocked.Load() {
			return nil, status.Error(codes.Unavailable, "test CP transport outage")
		}
		return handler(ctx, req)
	}))
	cp.RegisterControlPlaneAPIServer(cpSrv, cpapi.New(state, cpapi.WithLogger(quiet), cpapi.WithHeartbeatInterval(time.Second)))
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
	gl := []net.Listener{listen(t), listen(t), listen(t)}
	rl := []net.Listener{listen(t), listen(t), listen(t)}
	var peers []node.Peer
	for i := range gl {
		peers = append(peers, node.Peer{ID: uint64(i + 1), NodeID: fmt.Sprintf("n%d", i), ClientAddr: gl[i].Addr().String(), RaftURL: "http://" + rl[i].Addr().String() + "/raft"})
	}
	cfg := &cpconfig.Config{Revision: 1, PartitionCount: 1,
		Regions:    []cpconfig.Region{{ID: "a", Role: cpconfig.RegionRoleMaster}},
		Partitions: []cpconfig.PartitionAssignment{{Region: "a", PartitionID: 0, LeaderNodeID: peers[0].NodeID}},
	}
	for _, peer := range peers {
		cfg.Nodes = append(cfg.Nodes, cpconfig.Node{ID: peer.NodeID, Region: "a", Address: peer.ClientAddr})
		cfg.Partitions[0].ReplicaNodeIDs = append(cfg.Partitions[0].ReplicaNodeIDs, peer.NodeID)
	}
	if err := state.ApplyConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := poller.Refresh(ctx); err != nil {
		t.Fatal(err)
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
	// Record actual TCP storage destinations without replacing the storage API.
	clients := map[string]pb.CacheGatewayClient{}
	for _, peer := range peers {
		c, err := grpc.NewClient(peer.ClientAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients[peer.ClientAddr] = pb.NewCacheGatewayClient(c)
	}
	attempts := make(chan string, 100)
	fwd := &gateway.Forwarder{Router: router, Logger: quiet, Timeout: 2 * time.Second,
		Dial: func(addr string) (pb.CacheGatewayClient, error) {
			if clients[addr] == nil {
				return nil, fmt.Errorf("unconfigured destination %q", addr)
			}
			attempts <- addr
			return clients[addr], nil
		}}
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
	eventually(t, "initial elected leader hint", func() bool {
		if poller.Refresh(ctx) != nil {
			return false
		}
		route, err := router.WriteLeader(0)
		return err == nil && route.NodeID == peers[leader].NodeID && router.Revision() == 1
	})
	put, err := client.Put(ctx, &pb.PutRequest{Key: "static-hints", Data: []byte("confirmed")})
	if err != nil || put.Status != pb.PutStatus_PUT_STATUS_OK {
		t.Fatalf("Put %v %v", put, err)
	}

	// Fill the read preference as well as the write preference with the old leader.
	if got, err := client.Get(ctx, &pb.GetRequest{Key: "static-hints"}); err != nil || string(got.GetData()) != "confirmed" {
		t.Fatalf("initial read: %v %v", got, err)
	}
	for len(attempts) > 0 {
		<-attempts
	}
	oldTerm := p.nodes[leader].Status().Term
	p.nodes[leader].Close()
	<-p.runDone[leader]
	next := p.leaderIndex(t, leader)
	if next == leader || p.nodes[next].Status().Term <= oldTerm {
		t.Fatal("leader/term did not advance")
	}
	eventually(t, "same revision refresh observes new elected leader", func() bool {
		if poller.Refresh(ctx) != nil {
			return false
		}
		route, err := router.WriteLeader(0)
		rs, re := router.Replicas(0, true)
		return err == nil && re == nil && len(rs) == 3 && route.NodeID == peers[next].NodeID && router.Revision() == 1
	})
	// CP loss cannot revoke the static replicas or stop existing routes.
	blocked.Store(true)
	if err := poller.Refresh(ctx); err == nil {
		t.Fatal("CP outage not exercised")
	}
	got, err := client.Get(ctx, &pb.GetRequest{Key: "static-hints"})
	if err != nil || string(got.GetData()) != "confirmed" {
		t.Fatalf("failover Get %v %v", got, err)
	}
	if first := <-attempts; first != peers[next].ClientAddr {
		t.Fatalf("stale read preference hid newer CP hint: first %s, want %s", first, peers[next].ClientAddr)
	}
	for len(attempts) > 0 {
		<-attempts
	}
	updated, err := client.Put(ctx, &pb.PutRequest{Key: "static-hints", Data: []byte("after"), ExpectedRev: &put.Rev})
	if err != nil || updated.GetStatus() != pb.PutStatus_PUT_STATUS_OK || updated.GetRev() <= put.Rev {
		t.Fatalf("failover Put %v %v", updated, err)
	}
	if first := <-attempts; first != peers[next].ClientAddr {
		t.Fatalf("stale write preference hid newer CP hint: first %s, want %s", first, peers[next].ClientAddr)
	}
}
