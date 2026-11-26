package shardraft

import (
	"testing"

	"go.etcd.io/bbolt"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestDiskReplacesUncommittedSuffix(t *testing.T) {
	db, err := openDisk(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identity := []byte("test")
	rd := raft.Ready{HardState: raftpb.HardState{Term: 1, Vote: 1, Commit: 1}}
	for i := uint64(1); i <= 5; i++ {
		rd.Entries = append(rd.Entries, raftpb.Entry{Index: i, Term: 1})
	}
	if err := initDisk(db, identity, rd); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		return saveDisk(tx.Bucket(stateBucket), raft.Ready{
			HardState: raftpb.HardState{Term: 2, Vote: 2, Commit: 1},
			Entries:   []raftpb.Entry{{Index: 2, Term: 2}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	storage := raft.NewMemoryStorage()
	if _, err := loadDisk(db, identity, false, storage); err != nil {
		t.Fatal(err)
	}
	last, _ := storage.LastIndex()
	term, _ := storage.Term(2)
	hard, _, _ := storage.InitialState()
	if last != 2 || term != 2 || hard.Term != 2 || hard.Vote != 2 || hard.Commit != 1 {
		t.Fatalf("unexpected restored state: last=%d term=%d hard=%+v", last, term, hard)
	}
}
