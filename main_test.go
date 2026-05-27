package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStateFile points FLOX_AUTO_ACTIVATE_STATE_FILE at a fresh temp
// file for the duration of the test.
func withStateFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	t.Setenv("FLOX_AUTO_ACTIVATE_STATE_FILE", path)
	return path
}

func TestCmdAllow_ExistingPath(t *testing.T) {
	statePath := withStateFile(t)
	dir := t.TempDir()
	var out bytes.Buffer
	if err := cmdAllow([]string{dir}, &out); err != nil {
		t.Fatalf("cmdAllow: %v", err)
	}
	if !strings.Contains(out.String(), "allowed: ") {
		t.Fatalf("output: %q", out.String())
	}
	s, err := loadStateFrom(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status(dir) != StatusAllowed {
		t.Fatalf("expected allowed for %s, got status %v", dir, s.Status(dir))
	}
}

func TestCmdAllow_NonExistentPathWithoutPreapprove(t *testing.T) {
	withStateFile(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	var out bytes.Buffer
	err := cmdAllow([]string{missing}, &out)
	if err == nil {
		t.Fatalf("expected error for non-existent path, got nil; output=%q", out.String())
	}
	if !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "--preapprove") {
		t.Fatalf("error message should explain --preapprove, got: %v", err)
	}
}

func TestCmdAllow_NonExistentPathWithPreapprove(t *testing.T) {
	statePath := withStateFile(t)
	missing := filepath.Join(t.TempDir(), "future-clone-target")
	var out bytes.Buffer
	if err := cmdAllow([]string{"--preapprove", missing}, &out); err != nil {
		t.Fatalf("cmdAllow --preapprove: %v", err)
	}
	if !strings.Contains(out.String(), "preapproved: ") {
		t.Fatalf("expected preapproved output, got %q", out.String())
	}
	s, _ := loadStateFrom(statePath)
	if s.Status(missing) != StatusAllowed {
		t.Fatalf("expected allowed for %s, got %v", missing, s.Status(missing))
	}
}

func TestCmdAllow_UnknownFlag(t *testing.T) {
	withStateFile(t)
	var out bytes.Buffer
	err := cmdAllow([]string{"--nope", t.TempDir()}, &out)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected unknown flag error, got %v", err)
	}
}

func TestStateFilePermissions(t *testing.T) {
	statePath := withStateFile(t)
	var out bytes.Buffer
	if err := cmdAllow([]string{t.TempDir()}, &out); err != nil {
		t.Fatalf("cmdAllow: %v", err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	// 0o600: state file holds the user's project list and should not
	// be world- or group-readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state.json perm = %o, want 0600", perm)
	}
}

func TestSelfUpdate_RejectsBadVersionTag(t *testing.T) {
	// Server fails the test if it ever sees a request — invalid tags
	// must be rejected before any HTTP fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	defer srv.Close()
	s, _, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	cases := []string{
		"v../../admin",
		"vX.Y.Z",
		"latest",
		"1.2.3.4",
		"v1.2",
		"v1.2.3 extra",
		"v1.2.3/../foo",
	}
	for _, tag := range cases {
		err := s.run(context.Background(), selfUpdateOpts{Version: tag})
		if err == nil || !strings.Contains(err.Error(), "invalid --version") {
			t.Errorf("tag %q: expected invalid-version error, got %v", tag, err)
		}
	}
}

func TestSelfUpdate_AcceptsValidVersionTag(t *testing.T) {
	srv := fakeGitHub(t, "v1.0.0", "linux", "amd64", []byte("NEW"), "")
	defer srv.Close()
	s, _, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	// Valid tag, same version: should succeed with no-op message.
	if err := s.run(context.Background(), selfUpdateOpts{Version: "v1.0.0"}); err != nil {
		t.Fatalf("v1.0.0: %v", err)
	}
}

func TestSelfUpdate_RejectsOversizedDownload(t *testing.T) {
	// Server streams maxBinaryBytes+1 bytes; download should abort.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tag_name":"v2.0.0","assets":[` +
				`{"name":"flox-auto-activate-linux-amd64","browser_download_url":"http://` + r.Host + `/dl/bin"},` +
				`{"name":"flox-auto-activate-linux-amd64.sha256","browser_download_url":"http://` + r.Host + `/dl/sha"}` +
				`]}`))
		case r.URL.Path == "/dl/sha":
			// Any valid-length hex. We won't reach the compare anyway.
			w.Write([]byte(strings.Repeat("a", 64) + "  bin\n"))
		case r.URL.Path == "/dl/bin":
			// Stream maxBinaryBytes+1 zero bytes.
			buf := make([]byte, 1<<20)
			remaining := int64(maxBinaryBytes) + 1
			for remaining > 0 {
				n := int64(len(buf))
				if n > remaining {
					n = remaining
				}
				if _, err := w.Write(buf[:n]); err != nil {
					return
				}
				remaining -= n
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	err := s.run(context.Background(), selfUpdateOpts{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
	// Original binary must be untouched.
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLD" {
		t.Fatalf("selfPath changed: %q", got)
	}
	// No leftover temp file.
	entries, _ := os.ReadDir(filepath.Dir(selfPath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "flox-auto-activate.new.") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestSelfUpdate_TamperedTempFileRejected(t *testing.T) {
	srv := fakeGitHub(t, "v2.0.0", "linux", "amd64", []byte("NEW BINARY"), "")
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	// Intercept by wrapping replaceBinary's input: tamper with the
	// file on disk between the download hash and the verify step.
	// Easiest seam: simulate by overwriting after replaceBinary's
	// re-hash by running run() twice doesn't help — instead, drive
	// the lower-level path directly.
	rel, err := s.latestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	binURL, shaURL, err := assetFor(rel, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Dir(s.selfPath)
	tmpPath, wantHex, err := s.downloadAndVerify(context.Background(), binURL, shaURL, destDir)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the temp file.
	if err := os.WriteFile(tmpPath, []byte("EVIL"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = s.replaceBinary(tmpPath, s.selfPath, wantHex)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch on re-verify") {
		t.Fatalf("expected TOCTOU detection, got %v", err)
	}
	// selfPath untouched.
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLD" {
		t.Fatalf("selfPath changed: %q", got)
	}
}
