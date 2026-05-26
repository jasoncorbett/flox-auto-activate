package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s, err := loadStateFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Status("/a"); got != StatusUnknown {
		t.Fatalf("empty state: got %v want unknown", got)
	}
	s.Allow("/a/b")
	s.Deny("/c/d")
	if err := s.save(); err != nil {
		t.Fatal(err)
	}

	s2, err := loadStateFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Status("/a/b"); got != StatusAllowed {
		t.Fatalf("/a/b: got %v want allowed", got)
	}
	if got := s2.Status("/c/d"); got != StatusDenied {
		t.Fatalf("/c/d: got %v want denied", got)
	}
	if got := s2.Status("/nope"); got != StatusUnknown {
		t.Fatalf("/nope: got %v want unknown", got)
	}
}

func TestAllowRemovesFromDenied(t *testing.T) {
	dir := t.TempDir()
	s, _ := loadStateFrom(filepath.Join(dir, "state.json"))
	s.Deny("/x")
	s.Allow("/x")
	if s.Status("/x") != StatusAllowed {
		t.Fatalf("expected allowed")
	}
	if containsString(s.Denied, "/x") {
		t.Fatalf("/x still in Denied: %v", s.Denied)
	}
}

func TestDenyRemovesFromAllowed(t *testing.T) {
	dir := t.TempDir()
	s, _ := loadStateFrom(filepath.Join(dir, "state.json"))
	s.Allow("/x")
	s.Deny("/x")
	if s.Status("/x") != StatusDenied {
		t.Fatalf("expected denied")
	}
	if containsString(s.Allowed, "/x") {
		t.Fatalf("/x still in Allowed: %v", s.Allowed)
	}
}

func TestAllowDeduplicates(t *testing.T) {
	dir := t.TempDir()
	s, _ := loadStateFrom(filepath.Join(dir, "state.json"))
	s.Allow("/x")
	s.Allow("/x")
	s.Allow("/x/")
	if len(s.Allowed) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(s.Allowed), s.Allowed)
	}
}

func TestStateMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")
	s, err := loadStateFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Allowed) != 0 || len(s.Denied) != 0 {
		t.Fatalf("expected empty state")
	}
}

func TestServicesPref(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, _ := loadStateFrom(path)

	if enabled, set := s.ServicesPref("/a"); set || enabled {
		t.Fatalf("unset path: got (%v,%v), want (false,false)", enabled, set)
	}

	s.SetServices("/a", true)
	if enabled, set := s.ServicesPref("/a"); !set || !enabled {
		t.Fatalf("after on: got (%v,%v), want (true,true)", enabled, set)
	}

	s.SetServices("/a", false)
	if enabled, set := s.ServicesPref("/a"); !set || enabled {
		t.Fatalf("after off: got (%v,%v), want (false,true)", enabled, set)
	}

	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := loadStateFrom(path)
	if enabled, set := s2.ServicesPref("/a"); !set || enabled {
		t.Fatalf("after reload: got (%v,%v), want (false,true)", enabled, set)
	}

	s2.UnsetServices("/a")
	if enabled, set := s2.ServicesPref("/a"); set || enabled {
		t.Fatalf("after unset: got (%v,%v), want (false,false)", enabled, set)
	}
	if s2.Services != nil {
		t.Fatalf("expected map to be nilled when empty, got %v", s2.Services)
	}
}

func TestStateV1Compat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	v1 := `{"version":1,"allowed":["/a"],"denied":["/b"]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadStateFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status("/a") != StatusAllowed || s.Status("/b") != StatusDenied {
		t.Fatalf("v1 state did not parse correctly: %+v", s)
	}
	if _, set := s.ServicesPref("/a"); set {
		t.Fatalf("v1 state should have no services prefs")
	}
}

func TestStateAtomicWriteCleansTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, _ := loadStateFrom(path)
	s.Allow("/x")
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "state.json" {
			continue
		}
		t.Fatalf("unexpected file left behind: %s", e.Name())
	}
}
