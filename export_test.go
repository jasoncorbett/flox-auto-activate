package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkProj(t *testing.T) (root, sub string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, "proj")
	sub = filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".flox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, sub
}

func newState(t *testing.T) *State {
	t.Helper()
	dir := t.TempDir()
	s, err := loadStateFrom(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDecide_OuterNoFlox(t *testing.T) {
	tmp := t.TempDir()
	d := decide(map[string]string{}, tmp, newState(t))
	if d.Kind != decideNothing {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_OuterAllowed(t *testing.T) {
	root, _ := mkProj(t)
	s := newState(t)
	s.Allow(root)
	d := decide(map[string]string{}, root, s)
	if d.Kind != decideActivate || d.Path != root {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_OuterDenied(t *testing.T) {
	root, _ := mkProj(t)
	s := newState(t)
	s.Deny(root)
	d := decide(map[string]string{}, root, s)
	if d.Kind != decideNothing {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_OuterUnknown_NeedsPrompt(t *testing.T) {
	root, _ := mkProj(t)
	d := decide(map[string]string{}, root, newState(t))
	if d.Kind != decideNeedPrompt || d.Path != root {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_ForeignFloxPassthrough(t *testing.T) {
	root, _ := mkProj(t)
	d := decide(map[string]string{"FLOX_ENV": "/some/other"}, root, newState(t))
	if d.Kind != decideNothing {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_ManagedStayInRoot(t *testing.T) {
	root, sub := mkProj(t)
	env := map[string]string{"FLOX_AUTO_ACTIVATE_ROOT": root, "FLOX_AUTO_ACTIVATE_TMPFILE": "/tmp/tf"}
	d := decide(env, sub, newState(t))
	if d.Kind != decideNothing {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_ManagedCdOut(t *testing.T) {
	root, _ := mkProj(t)
	other := t.TempDir()
	env := map[string]string{"FLOX_AUTO_ACTIVATE_ROOT": root, "FLOX_AUTO_ACTIVATE_TMPFILE": "/tmp/tf"}
	d := decide(env, other, newState(t))
	if d.Kind != decideExit || d.Target != other || d.TmpFile != "/tmp/tf" {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_ManagedNestedAllowed(t *testing.T) {
	root, _ := mkProj(t)
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".flox"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newState(t)
	s.Allow(inner)
	env := map[string]string{"FLOX_AUTO_ACTIVATE_ROOT": root, "FLOX_AUTO_ACTIVATE_TMPFILE": "/tmp/tf"}
	d := decide(env, inner, s)
	if d.Kind != decideActivate || d.Path != inner {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_ManagedNestedNeedsPrompt(t *testing.T) {
	root, _ := mkProj(t)
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".flox"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FLOX_AUTO_ACTIVATE_ROOT": root, "FLOX_AUTO_ACTIVATE_TMPFILE": "/tmp/tf"}
	d := decide(env, inner, newState(t))
	if d.Kind != decideNeedPrompt || d.Path != inner {
		t.Fatalf("got %+v", d)
	}
}

func TestEmit_ActivateNoServices(t *testing.T) {
	s := emit(Decision{Kind: decideActivate, Path: "/tmp/fx test"})
	if !strings.Contains(s, "flox activate -d '/tmp/fx test'") {
		t.Fatalf("expected activate without -s: %q", s)
	}
	if strings.Contains(s, "flox activate -s") {
		t.Fatalf("did not expect -s flag: %q", s)
	}
	if !strings.Contains(s, "FLOX_AUTO_ACTIVATE_ROOT='/tmp/fx test'") {
		t.Fatalf("missing ROOT assignment: %q", s)
	}
}

func TestEmit_ActivateWithServices(t *testing.T) {
	s := emit(Decision{Kind: decideActivate, Path: "/tmp/p", StartServices: true})
	if !strings.Contains(s, "flox activate -s -d '/tmp/p'") {
		t.Fatalf("expected activate with -s: %q", s)
	}
}

func TestDecide_OuterAllowedWithServicesOn(t *testing.T) {
	root, _ := mkProj(t)
	s := newState(t)
	s.Allow(root)
	s.SetServices(root, true)
	d := decide(map[string]string{}, root, s)
	if d.Kind != decideActivate || !d.StartServices {
		t.Fatalf("got %+v", d)
	}
}

func TestDecide_OuterAllowedWithServicesOff(t *testing.T) {
	root, _ := mkProj(t)
	s := newState(t)
	s.Allow(root)
	s.SetServices(root, false)
	d := decide(map[string]string{}, root, s)
	if d.Kind != decideActivate || d.StartServices {
		t.Fatalf("got %+v", d)
	}
}

func TestEmit_Exit(t *testing.T) {
	s := emit(Decision{Kind: decideExit, Target: "/elsewhere", TmpFile: "/tmp/x"})
	if !strings.Contains(s, "printf %s '/elsewhere' > '/tmp/x'") {
		t.Fatalf("missing tmpfile write: %q", s)
	}
	if !strings.Contains(s, "exit 0") {
		t.Fatalf("missing exit: %q", s)
	}
}

func TestEmit_QuotingTrickyPath(t *testing.T) {
	s := emit(Decision{Kind: decideActivate, Path: "/a's/b"})
	if !strings.Contains(s, `'/a'\''s/b'`) {
		t.Fatalf("bad quoting: %q", s)
	}
}
