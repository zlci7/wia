package memory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/session"
)

func TestInMemoryStoreRecentReturnsLatestRecordsInAppendOrder(t *testing.T) {
	store := memory.NewInMemoryStore()
	key := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Abigail"}

	appendRecords(t, store, key, "mem-1", "mem-2", "mem-3")

	got, err := store.Recent(context.Background(), key, 2)
	if err != nil {
		t.Fatalf("Recent returned error: %v", err)
	}

	assertMemoryIDs(t, got, []string{"mem-2", "mem-3"})
}

func TestInMemoryStoreRecentUsesAppendOrderWhenCreatedAtMatches(t *testing.T) {
	store := memory.NewInMemoryStore()
	key := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Abigail"}
	sameTime := time.Unix(100, 0)

	for _, id := range []string{"mem-1", "mem-2", "mem-3"} {
		if err := store.Append(context.Background(), memory.Record{
			MemoryID:   id,
			SessionKey: key,
			CreatedAt:  sameTime,
		}); err != nil {
			t.Fatalf("Append(%s) returned error: %v", id, err)
		}
	}

	got, err := store.Recent(context.Background(), key, 3)
	if err != nil {
		t.Fatalf("Recent returned error: %v", err)
	}

	assertMemoryIDs(t, got, []string{"mem-1", "mem-2", "mem-3"})
}

func TestInMemoryStorePrunesOldRecordsAfterDefaultLimit(t *testing.T) {
	store := memory.NewInMemoryStore()
	key := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Abigail"}

	var ids []string
	for i := 1; i <= 25; i++ {
		ids = append(ids, fmt.Sprintf("mem-%02d", i))
	}
	appendRecords(t, store, key, ids...)

	got, err := store.Recent(context.Background(), key, 100)
	if err != nil {
		t.Fatalf("Recent returned error: %v", err)
	}

	assertMemoryIDs(t, got, []string{
		"mem-06", "mem-07", "mem-08", "mem-09", "mem-10",
		"mem-11", "mem-12", "mem-13", "mem-14", "mem-15",
		"mem-16", "mem-17", "mem-18", "mem-19", "mem-20",
		"mem-21", "mem-22", "mem-23", "mem-24", "mem-25",
	})
}

func TestInMemoryStoreIsolatesByAgentSessionKey(t *testing.T) {
	store := memory.NewInMemoryStore()
	abigail := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Abigail"}
	linus := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Linus"}
	otherWorldAbigail := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-b", EntityID: "npc:Abigail"}

	appendRecords(t, store, abigail, "abigail-world-a")
	appendRecords(t, store, linus, "linus-world-a")
	appendRecords(t, store, otherWorldAbigail, "abigail-world-b")

	got, err := store.Recent(context.Background(), abigail, 10)
	if err != nil {
		t.Fatalf("Recent returned error: %v", err)
	}

	assertMemoryIDs(t, got, []string{"abigail-world-a"})
}

func TestInMemoryStoreRejectsEmptyMemoryID(t *testing.T) {
	store := memory.NewInMemoryStore()
	key := session.AgentSessionKey{GameID: "stardew-valley", WorldID: "world-a", EntityID: "npc:Abigail"}

	err := store.Append(context.Background(), memory.Record{SessionKey: key})
	if !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("Append error = %v, want ErrInvalidRecord", err)
	}
}

func appendRecords(t *testing.T, store memory.Store, key session.AgentSessionKey, ids ...string) {
	t.Helper()

	for i, id := range ids {
		if err := store.Append(context.Background(), memory.Record{
			MemoryID:   id,
			SessionKey: key,
			CreatedAt:  time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("Append(%s) returned error: %v", id, err)
		}
	}
}

func assertMemoryIDs(t *testing.T, got []memory.Record, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got=%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].MemoryID != want[i] {
			t.Fatalf("got[%d].MemoryID = %q, want %q; got=%+v", i, got[i].MemoryID, want[i], got)
		}
	}
}
