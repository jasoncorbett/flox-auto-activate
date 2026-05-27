package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		err  bool
	}{
		{"1.0.0", "1.0.0", 0, false},
		{"1.0.0", "1.0.1", -1, false},
		{"1.0.1", "1.0.0", 1, false},
		{"v1.0.0", "v1.0.0", 0, false},
		{"v2.0.0", "v1.9.9", 1, false},
		{"1.10.0", "1.9.0", 1, false},
		{"1.0.0-beta", "1.0.0", 0, false},
		{"v1.0.0+meta", "v1.0.0", 0, false},
		{"1.0", "1.0.0", 0, true},
		{"abc", "1.0.0", 0, true},
		{"1.0.0", "1.x.0", 0, true},
	}
	for _, tc := range cases {
		got, err := compareSemver(tc.a, tc.b)
		if tc.err {
			if err == nil {
				t.Errorf("compareSemver(%q,%q) expected error, got %d", tc.a, tc.b, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("compareSemver(%q,%q) unexpected error: %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAssetFor(t *testing.T) {
	r := &release{
		TagName: "v1.0.0",
		Assets: []releaseAsset{
			{Name: "flox-auto-activate-linux-amd64", DownloadURL: "u1"},
			{Name: "flox-auto-activate-linux-amd64.sha256", DownloadURL: "u2"},
			{Name: "flox-auto-activate-darwin-arm64", DownloadURL: "u3"},
			{Name: "flox-auto-activate-darwin-arm64.sha256", DownloadURL: "u4"},
		},
	}
	bin, sha, err := assetFor(r, "linux", "amd64")
	if err != nil || bin != "u1" || sha != "u2" {
		t.Fatalf("linux/amd64: got (%q,%q,%v)", bin, sha, err)
	}
	bin, sha, err = assetFor(r, "darwin", "arm64")
	if err != nil || bin != "u3" || sha != "u4" {
		t.Fatalf("darwin/arm64: got (%q,%q,%v)", bin, sha, err)
	}
	if _, _, err := assetFor(r, "windows", "amd64"); err == nil {
		t.Fatalf("expected error for unpublished combo")
	}
}

func TestAssetFor_MissingChecksum(t *testing.T) {
	r := &release{
		TagName: "v1.0.0",
		Assets: []releaseAsset{
			{Name: "flox-auto-activate-linux-amd64", DownloadURL: "u1"},
		},
	}
	if _, _, err := assetFor(r, "linux", "amd64"); err == nil {
		t.Fatalf("expected error when .sha256 sibling is missing")
	}
}

// fakeGitHub returns an httptest.Server that serves a single release
// with a single os/arch asset pair. binBody is the content we'll
// serve as the binary; shaHex overrides the checksum (use "" for the
// correct one).
func fakeGitHub(t *testing.T, tag, goos, goarch string, binBody []byte, shaHex string) *httptest.Server {
	t.Helper()
	if shaHex == "" {
		sum := sha256.Sum256(binBody)
		shaHex = hex.EncodeToString(sum[:])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases/tags/"+tag):
			rel := release{
				TagName: tag,
				Assets: []releaseAsset{
					{
						Name:        fmt.Sprintf("flox-auto-activate-%s-%s", goos, goarch),
						DownloadURL: "REPLACE_BIN",
					},
					{
						Name:        fmt.Sprintf("flox-auto-activate-%s-%s.sha256", goos, goarch),
						DownloadURL: "REPLACE_SHA",
					},
				},
			}
			body, _ := json.Marshal(&rel)
			// Replace placeholders with real URLs that point back at us.
			body = bytes.ReplaceAll(body, []byte("REPLACE_BIN"), []byte(r.Host+":bin"))
			body = bytes.ReplaceAll(body, []byte("REPLACE_SHA"), []byte(r.Host+":sha"))
			// Use absolute URLs back to this server.
			body = bytes.ReplaceAll(body, []byte(r.Host+":bin"), []byte("http://"+r.Host+"/dl/bin"))
			body = bytes.ReplaceAll(body, []byte(r.Host+":sha"), []byte("http://"+r.Host+"/dl/sha"))
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		case r.URL.Path == "/dl/bin":
			w.Write(binBody)
		case r.URL.Path == "/dl/sha":
			fmt.Fprintf(w, "%s  flox-auto-activate-%s-%s\n", shaHex, goos, goarch)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func newSelfUpdate(t *testing.T, srv *httptest.Server, currentVer, goos, goarch string) (*selfUpdate, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	selfPath := filepath.Join(dir, "fxa")
	if err := os.WriteFile(selfPath, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s := &selfUpdate{
		apiBase:    srv.URL,
		repo:       "test/test",
		http:       srv.Client(),
		currentVer: currentVer,
		goos:       goos,
		goarch:     goarch,
		selfPath:   selfPath,
		out:        &buf,
	}
	return s, selfPath, &buf
}

func TestRun_CheckUpToDate(t *testing.T) {
	srv := fakeGitHub(t, "v1.0.0", "linux", "amd64", []byte("NEW"), "")
	defer srv.Close()
	s, selfPath, out := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	err := s.run(context.Background(), selfUpdateOpts{Check: true})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Fatalf("output: %q", out.String())
	}
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLD" {
		t.Fatalf("selfPath should be untouched, got %q", got)
	}
}

func TestRun_CheckUpdateAvailable(t *testing.T) {
	srv := fakeGitHub(t, "v1.1.0", "linux", "amd64", []byte("NEW"), "")
	defer srv.Close()
	s, _, out := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	err := s.run(context.Background(), selfUpdateOpts{Check: true})
	if !errors.Is(err, errUpdateAvailable) {
		t.Fatalf("expected errUpdateAvailable, got %v", err)
	}
	if !strings.Contains(out.String(), "update available: v1.1.0") {
		t.Fatalf("output: %q", out.String())
	}
}

func TestRun_FullFlow(t *testing.T) {
	srv := fakeGitHub(t, "v2.0.0", "linux", "amd64", []byte("NEW BINARY"), "")
	defer srv.Close()
	s, selfPath, out := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	if err := s.run(context.Background(), selfUpdateOpts{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "installed v2.0.0") {
		t.Fatalf("output: %q", out.String())
	}
	got, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW BINARY" {
		t.Fatalf("selfPath content = %q, want NEW BINARY", got)
	}
	info, err := os.Stat(selfPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced binary is not executable: mode=%v", info.Mode())
	}
}

func TestRun_ChecksumMismatch(t *testing.T) {
	srv := fakeGitHub(t, "v2.0.0", "linux", "amd64", []byte("NEW BINARY"), strings.Repeat("0", 64))
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	err := s.run(context.Background(), selfUpdateOpts{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	// Self file untouched.
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLD" {
		t.Fatalf("selfPath changed despite mismatch: %q", got)
	}
	// No leftover .new file.
	entries, _ := os.ReadDir(filepath.Dir(selfPath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "flox-auto-activate.new.") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestRun_DevBuildRefused(t *testing.T) {
	srv := fakeGitHub(t, "v1.0.0", "linux", "amd64", []byte("NEW"), "")
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "dev", "linux", "amd64")
	err := s.run(context.Background(), selfUpdateOpts{})
	if err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Fatalf("expected dev-build refusal, got %v", err)
	}
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLD" {
		t.Fatalf("selfPath should be untouched, got %q", got)
	}
}

func TestRun_DevBuildForce(t *testing.T) {
	srv := fakeGitHub(t, "v1.0.0", "linux", "amd64", []byte("NEW"), "")
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "dev", "linux", "amd64")
	if err := s.run(context.Background(), selfUpdateOpts{Force: true}); err != nil {
		t.Fatalf("run --force: %v", err)
	}
	got, _ := os.ReadFile(selfPath)
	if string(got) != "NEW" {
		t.Fatalf("expected NEW, got %q", got)
	}
}

func TestRun_VersionPin(t *testing.T) {
	srv := fakeGitHub(t, "v0.9.0", "linux", "amd64", []byte("OLDER"), "")
	defer srv.Close()
	s, selfPath, _ := newSelfUpdate(t, srv, "1.0.0", "linux", "amd64")
	// Without --force, refuses to downgrade.
	err := s.run(context.Background(), selfUpdateOpts{Version: "v0.9.0"})
	if err == nil || !strings.Contains(err.Error(), "newer than") {
		t.Fatalf("expected newer-than refusal, got %v", err)
	}
	// With --force, downgrades.
	if err := s.run(context.Background(), selfUpdateOpts{Version: "v0.9.0", Force: true}); err != nil {
		t.Fatalf("run --force --version: %v", err)
	}
	got, _ := os.ReadFile(selfPath)
	if string(got) != "OLDER" {
		t.Fatalf("expected OLDER, got %q", got)
	}
}
