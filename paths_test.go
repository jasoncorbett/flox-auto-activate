package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsUnder(t *testing.T) {
	cases := []struct {
		name   string
		child  string
		parent string
		want   bool
	}{
		{"equal", "/a/b", "/a/b", true},
		{"direct child", "/a/b/c", "/a/b", true},
		{"deep child", "/a/b/c/d/e", "/a/b", true},
		{"sibling shared prefix", "/a/bb", "/a/b", false},
		{"unrelated", "/x/y", "/a/b", false},
		{"trailing slash parent", "/a/b/c", "/a/b/", true},
		{"dotty paths cleaned", "/a/./b/../b/c", "/a/b", true},
		{"root", "/", "/", true},
		{"root child", "/a", "/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnder(tc.child, tc.parent); got != tc.want {
				t.Fatalf("isUnder(%q,%q)=%v want %v", tc.child, tc.parent, got, tc.want)
			}
		})
	}
}

func TestFindFloxRoot(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(filepath.Join(proj, ".flox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if root, ok := findFloxRoot(sub); !ok || root != proj {
		t.Fatalf("from sub: got (%q,%v), want (%q,true)", root, ok, proj)
	}
	if root, ok := findFloxRoot(proj); !ok || root != proj {
		t.Fatalf("from proj: got (%q,%v), want (%q,true)", root, ok, proj)
	}
	if _, ok := findFloxRoot(tmp); ok {
		t.Fatalf("from tmp: expected not found")
	}
}

func TestFindFloxRoot_FloxIsFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, ".flox"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := findFloxRoot(tmp); ok {
		t.Fatalf(".flox as file should not be detected as root")
	}
}
