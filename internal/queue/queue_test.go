package queue

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUpsertDedupAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	d := Draft{
		PaneID:         "w1:p2",
		TabID:          "w1:t1",
		WorkspaceID:    "w1",
		Agent:          "grok",
		Name:           "reviewer",
		HerdrStatus:    "blocked",
		Kind:           KindNeedsDecision,
		StateChangeSeq: 4,
		Title:          "Allow edit?",
		Excerpt:        "Allow edit to file?",
	}
	it, res, err := st.Upsert(d, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || !res.ShouldNotify || it.ID != 1 {
		t.Fatalf("create: %+v %+v", it, res)
	}

	_, res, err = st.Upsert(d, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip, got %+v", res)
	}

	d.StateChangeSeq = 5
	d.Excerpt = "Allow network?"
	it, res, err = st.Upsert(d, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || it.ID != 1 || it.Excerpt != "Allow network?" || !res.ShouldNotify {
		t.Fatalf("update: %+v %+v", it, res)
	}

	open, err := st.List(false)
	if err != nil || len(open) != 1 {
		t.Fatalf("list open: %v %+v", err, open)
	}

	acked, err := st.Ack(1, now)
	if err != nil || acked.AckedAt == nil {
		t.Fatalf("ack: %+v %v", acked, err)
	}
	open, err = st.List(false)
	if err != nil || len(open) != 0 {
		t.Fatalf("list after ack: %v %+v", err, open)
	}

	d.StateChangeSeq = 6
	it, res, err = st.Upsert(d, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || it.ID != 2 {
		t.Fatalf("new after ack: %+v %+v", it, res)
	}

	if _, err := st.Dismiss(2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Dismiss(2, now); err == nil {
		t.Fatal("expected conflict on second dismiss")
	}

	all, err := st.List(true)
	if err != nil || len(all) != 2 {
		t.Fatalf("list all: %v %+v", err, all)
	}

	got, err := st.Get(2)
	if err != nil || got.DismissedAt == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
}

func TestParseID(t *testing.T) {
	if _, err := ParseID("0"); err == nil {
		t.Fatal("expected error")
	}
	n, err := ParseID("12")
	if err != nil || n != 12 {
		t.Fatalf("got %d %v", n, err)
	}
}
