package shardraft_test

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/OraCache/oracache/gen/gateway/v1"
	"github.com/OraCache/oracache/internal/cache"
	"github.com/OraCache/oracache/shardraft"
)

func TestPersistentCheckpointAndTail(t *testing.T) {
	cfg := testConfig(1, 1)
	cfg.DataDir, cfg.SnapshotEntries = t.TempDir(), 3
	open := func(cfg shardraft.Config) (*shardraft.Instance, error) {
		return shardraft.NewInstance(cfg, shardraft.AdaptCache(cache.New()), shardraft.NewMemoryNetwork())
	}
	refuse := func(cfg shardraft.Config) {
		t.Helper()
		if n, err := open(cfg); err == nil {
			_ = n.Close()
			t.Fatal("unexpectedly opened Raft state")
		}
	}
	refuse(cfg) // A missing file must not make a new voter.
	path := filepath.Join(cfg.DataDir, "raft.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("restart touched missing disk: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	refuse(cfg)
	cfg.Init = true
	refuse(cfg)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	n, err := open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runNode(t, n)
	c := &cluster{nodes: []*shardraft.Instance{n}}
	c.leader(t, 0)
	first := put(t, n, "snapshot", "before-compaction")
	eventually(t, "checkpoint at first write", func() bool { return n.Status().SnapshotIndex == 3 })
	tail := put(t, n, "tail", "after-compaction")
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	refuse(cfg) // Explicit initialization must not overwrite data, even offline.
	cfg.Init = false
	wrong := cfg
	wrong.GroupID += "-other"
	refuse(wrong)

	reopened, err := open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st := reopened.Status(); st.SnapshotIndex != 3 || st.CommitIndex != 4 || !st.Member || st.Ready {
		t.Fatalf("unexpected restored status: %+v", st)
	}
	runNode(t, reopened)
	c.nodes[0] = reopened
	c.leader(t, 0)
	for _, want := range []struct {
		key, value string
		rev        uint64
	}{{"snapshot", "before-compaction", first.GetRev()}, {"tail", "after-compaction", tail.GetRev()}} {
		got, err := reopened.Get(t.Context(), &pb.GetRequest{Key: want.key})
		if err != nil || got.GetStatus() != pb.GetStatus_GET_STATUS_HIT || got.GetRev() != want.rev || string(got.GetData()) != want.value {
			t.Fatalf("read %s: %v (%v)", want.key, got, err)
		}
	}
	if got := put(t, reopened, "tail", "write"); got.GetRev() != tail.GetRev()+1 {
		t.Fatalf("revision did not resume from tail: %v", got)
	}
}
