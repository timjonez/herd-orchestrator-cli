package spaces

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPutListGetRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spaces.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	sp, err := st.Put(Space{
		WorkspaceID: "w3",
		PaneID:      "w3:p1",
		Name:        "fix-login",
		Kind:        "claude",
		Label:       "fix-login",
		Cwd:         "/tmp/proj",
		Auto:        true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if sp.CreatedAt != now {
		t.Fatalf("created: %s", sp.CreatedAt)
	}

	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Name != "fix-login" {
		t.Fatalf("list: %v %+v", err, list)
	}

	got, err := st.Get("fix-login")
	if err != nil || got.WorkspaceID != "w3" {
		t.Fatalf("get name: %v %+v", err, got)
	}
	got, err = st.Get("w3")
	if err != nil || got.Name != "fix-login" {
		t.Fatalf("get id: %v %+v", err, got)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = st2.Get("w3")
	if err != nil || got.Cwd != "/tmp/proj" || !got.Auto {
		t.Fatalf("reopen: %v %+v", err, got)
	}

	if _, err := st.Remove("w3"); err != nil {
		t.Fatal(err)
	}
	list, err = st.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("list after remove: %v %+v", err, list)
	}
	if _, err := st.Get("w3"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestGetUnknown(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "spaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("w9"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPutReplacesSameWorkspace(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "spaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	if _, err := st.Put(Space{WorkspaceID: "w3", Name: "a"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(Space{WorkspaceID: "w3", Name: "b"}, now); err != nil {
		t.Fatal(err)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Name != "b" {
		t.Fatalf("replace: %v %+v", err, list)
	}
}
