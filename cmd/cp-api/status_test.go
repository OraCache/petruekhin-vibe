package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	cp "github.com/OraCache/oracache/gen/controlplane/v1"
	"github.com/OraCache/oracache/internal/cpapi"
	"github.com/OraCache/oracache/internal/cpconfig"
	"github.com/OraCache/oracache/internal/statestore"
)

func TestStatusHandlerConfigAndLeaderHintsReadOnly(t *testing.T) {
	ctx := context.Background()
	state := statestore.NewMemory()
	cfg := &cpconfig.Config{Revision: 1, PartitionCount: 1,
		Regions:    []cpconfig.Region{{ID: "r", Role: cpconfig.RegionRoleMaster}},
		Nodes:      []cpconfig.Node{{ID: "n1", Region: "r", Address: "a"}, {ID: "n2", Region: "r", Address: "b"}},
		Partitions: []cpconfig.PartitionAssignment{{Region: "r", LeaderNodeID: "n1", ReplicaNodeIDs: []string{"n1", "n2"}}},
	}
	if err := state.ApplyConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	api := cpapi.New(state)
	if _, err := api.ReportHeartbeat(ctx, &cp.HeartbeatRequest{NodeId: "n2", Region: "r", Partitions: []*cp.PartitionStatus{{Role: cp.RaftRole_RAFT_ROLE_LEADER, Term: 3, LeaderId: "n2", Ready: true}}}); err != nil {
		t.Fatal(err)
	}
	handler := statusHandler(api)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/heartbeats", nil))
		var got struct {
			ConfigRevision uint64            `json:"config_revision"`
			Config         *cp.ClusterConfig `json:"config"`
			LeaderHints    []*cp.LeaderHint  `json:"leader_hints"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || got.ConfigRevision != 1 || len(got.Config.GetNodes()) != 2 || len(got.LeaderHints) != 1 || got.LeaderHints[0].NodeId != "n2" || got.LeaderHints[0].Term != 3 {
			t.Fatalf("unexpected view: %s", rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/heartbeats", nil))
	if rec.Code != 405 {
		t.Fatalf("write allowed: %d", rec.Code)
	}
	snapshot, _ := state.Config(ctx)
	if snapshot.Revision != 1 {
		t.Fatal("diagnostics changed config")
	}
}

func TestStatusHandlerWithoutConfig(t *testing.T) {
	rec := httptest.NewRecorder()
	statusHandler(cpapi.New(statestore.NewMemory())).ServeHTTP(rec, httptest.NewRequest("GET", "/heartbeats", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
}
