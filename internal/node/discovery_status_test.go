package node_test

import (
	"context"
	"github.com/OraCache/oracache/internal/node"
	"github.com/OraCache/oracache/internal/store"
	"testing"
)

func TestNodeReportsDiscoveryGroup(t *testing.T) {
	gl, rl := listen(t), listen(t)
	n, err := node.New(node.Config{NodeID: "n", Region: "a", GroupID: "g", PeerID: 1, Peers: []node.Peer{{ID: 1, NodeID: "n", ClientAddr: gl.Addr().String(), RaftURL: "http://" + rl.Addr().String() + "/raft"}}, GRPCListener: gl, RaftListener: rl, Store: store.Config{MaxVersions: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	ss, err := n.PartitionStatuses(context.Background())
	if err != nil || len(ss) != 1 || ss[0].GroupId != "g" {
		t.Fatalf("%v %v", ss, err)
	}
}
