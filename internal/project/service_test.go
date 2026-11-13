package project

import (
	"os"
	"path/filepath"
	"testing"

	"seriallens/internal/events"
)

func newTestService(t *testing.T) (*Service, *[]string) {
	fired := &[]string{}
	emit := func(event string, payload map[string]any) {
		*fired = append(*fired, event)
	}
	return NewService(NewRecentStore(t.TempDir()), emit), fired
}

func TestServiceInitialNoProject(t *testing.T) {
	s, _ := newTestService(t)
	if s.State() != StateNoProject {
		t.Errorf("initial state = %v", s.State())
	}
	if _, _, ok := s.Current(); ok {
		t.Error("no project expected")
	}
}

func TestServiceCreateOpensProject(t *testing.T) {
	s, _ := newTestService(t)
	dir := filepath.Join(t.TempDir(), "P1")
	if err := s.Create(dir, "P1", "desc"); err != nil {
		t.Fatal(err)
	}
	if s.State() != StateProjectOpen {
		t.Errorf("state = %v", s.State())
	}
	d, m, ok := s.Current()
	if !ok || d != dir || m.Name != "P1" {
		t.Errorf("current = %v %v %v", d, m, ok)
	}
}

func TestServiceOpenEmitsAndRecords(t *testing.T) {
	s, fired := newTestService(t)
	dir := filepath.Join(t.TempDir(), "P1")
	if _, err := Scaffold(dir, "P1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(dir); err != nil {
		t.Fatal(err)
	}
	if len(*fired) != 1 || (*fired)[0] != events.EvProjectOpened {
		t.Errorf("events = %v", *fired)
	}
	if s.recents == nil {
		t.Fatal("recents store missing")
	}
	found := false
	for _, e := range s.recents.List() {
		if e.Path == dir {
			found = true
		}
	}
	if !found {
		t.Error("open must record into recent list")
	}
}

func TestServiceOpenBadKeepsCurrent(t *testing.T) {
	s, fired := newTestService(t)
	good := filepath.Join(t.TempDir(), "good")
	if _, err := Scaffold(good, "good", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(good); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "project.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(bad); err == nil {
		t.Fatal("open corrupt project must fail")
	}
	if d, _, _ := s.Current(); d != good {
		t.Errorf("current must stay %s, got %s", good, d)
	}
	last := (*fired)[len(*fired)-1]
	if last != events.EvProjectError {
		t.Errorf("last event = %s, want %s", last, events.EvProjectError)
	}
}

func TestServiceClose(t *testing.T) {
	s, fired := newTestService(t)
	dir := filepath.Join(t.TempDir(), "P1")
	if err := s.Create(dir, "P1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.State() != StateNoProject {
		t.Errorf("state = %v", s.State())
	}
	last := (*fired)[len(*fired)-1]
	if last != events.EvProjectClosed {
		t.Errorf("last event = %s", last)
	}
}

func TestServiceUnicodePath(t *testing.T) {
	s, _ := newTestService(t)
	dir := filepath.Join(t.TempDir(), "测试 工程 #1")
	if err := s.Open(dir); err == nil {
		t.Fatal("scaffold-not-created dir should fail; create first")
	}
	if err := s.Create(dir, "U", ""); err != nil {
		t.Fatal(err)
	}
	d, _, ok := s.Current()
	if !ok || d != dir {
		t.Errorf("current = %s", d)
	}
}
