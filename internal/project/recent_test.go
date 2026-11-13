package project

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *RecentStore {
	return NewRecentStore(t.TempDir())
}

func TestRecentListOrderNewestFirst(t *testing.T) {
	r := newStore(t)
	r.RecordOpen("/a", "A")
	time.Sleep(10 * time.Millisecond)
	r.RecordOpen("/b", "B")
	time.Sleep(10 * time.Millisecond)
	r.RecordOpen("/c", "C")
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Path != "/c" || list[2].Path != "/a" {
		t.Errorf("order = %s,%s,%s; want newest first", list[0].Path, list[1].Path, list[2].Path)
	}
}

func TestRecentCapTwentyKeepsPinned(t *testing.T) {
	r := newStore(t)
	for i := 0; i < 20; i++ {
		r.RecordOpen("/" + string(rune('a'+i)), "")
		time.Sleep(5 * time.Millisecond)
	}
	r.SetPinned("/a", true)
	r.RecordOpen("/new", "new")
	list := r.List()
	if len(list) != 20 {
		t.Fatalf("len = %d, want 20", len(list))
	}
	foundPinned, foundNew := false, false
	for _, e := range list {
		if e.Path == "/a" && e.Pinned {
			foundPinned = true
		}
		if e.Path == "/new" {
			foundNew = true
		}
	}
	if !foundPinned || !foundNew {
		t.Errorf("pinned=%v new=%v", foundPinned, foundNew)
	}
}

func TestRecentPinnedFirst(t *testing.T) {
	r := newStore(t)
	r.RecordOpen("/a", "A")
	time.Sleep(5 * time.Millisecond)
	r.RecordOpen("/b", "B")
	r.SetPinned("/a", true)
	list := r.List()
	if list[0].Path != "/a" {
		t.Errorf("pinned entry must be first, got %s", list[0].Path)
	}
}

func TestRecentRemove(t *testing.T) {
	r := newStore(t)
	r.RecordOpen("/a", "A")
	r.Remove("/a")
	if len(r.List()) != 0 {
		t.Error("remove failed")
	}
}

func TestRecentValidateMarksMissing(t *testing.T) {
	r := newStore(t)
	realDir := filepath.Join(t.TempDir(), "real")
	if _, err := Scaffold(realDir, "real", ""); err != nil {
		t.Fatal(err)
	}
	r.RecordOpen(realDir, "real")
	r.RecordOpen("/ghost", "ghost")
	r.Validate()
	list := r.List()
	byPath := map[string]RecentEntry{}
	for _, e := range list {
		byPath[e.Path] = e
	}
	if byPath[realDir].Missing {
		t.Error("valid project must not be missing")
	}
	if !byPath["/ghost"].Missing {
		t.Error("nonexistent path must be marked missing")
	}
	if len(list) != 2 {
		t.Error("validate must not delete entries")
	}
}

func TestRecentCorruptFileYieldsEmpty(t *testing.T) {
	home := t.TempDir()
	r := NewRecentStore(home)
	r.RecordOpen("/a", "A")
	// 覆写为损坏 JSON
	if err := writeFileRaw(filepath.Join(home, "recent_projects.json"), []byte("{corrupt")); err != nil {
		t.Fatal(err)
	}
	if list := r.List(); len(list) != 0 {
		t.Errorf("corrupt file must yield empty list, got %d", len(list))
	}
}
