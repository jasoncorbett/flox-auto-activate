package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// maxBinaryBytes caps the self-update download so a hijacked or
// misconfigured release endpoint can't fill the install partition
// before the HTTP timeout fires. The released binary is ~9MB; 200MiB
// is a comfortable ceiling.
const maxBinaryBytes = 200 << 20

// versionTagRe matches the tag form GitHub releases ship for this
// project: optional leading 'v', MAJOR.MINOR.PATCH, optional
// pre-release/build suffix. Anything that doesn't match never gets
// pasted into a URL path.
var versionTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

// errUpdateAvailable signals --check found a newer release. main.go
// translates this into exit code 10.
var errUpdateAvailable = errors.New("update available")

type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type selfUpdate struct {
	apiBase    string
	repo       string
	http       *http.Client
	currentVer string
	goos       string
	goarch     string
	selfPath   string
	out        io.Writer
}

type selfUpdateOpts struct {
	Check   bool
	Force   bool
	Version string // e.g. "v1.2.3"; empty means latest
}

func (s *selfUpdate) run(ctx context.Context, opts selfUpdateOpts) error {
	var (
		rel *release
		err error
	)
	if opts.Version != "" {
		if !versionTagRe.MatchString(opts.Version) {
			return fmt.Errorf("invalid --version %q: expected vMAJOR.MINOR.PATCH (e.g. v1.2.3)", opts.Version)
		}
		rel, err = s.releaseByTag(ctx, opts.Version)
	} else {
		rel, err = s.latestRelease(ctx)
	}
	if err != nil {
		return err
	}

	dev := s.currentVer == "dev"
	cmp, cmpErr := compareSemver(s.currentVer, rel.TagName)
	if cmpErr != nil && !dev {
		return fmt.Errorf("compare versions: %w", cmpErr)
	}
	if dev {
		cmp = -1 // treat dev as older than anything
	}

	if opts.Check {
		if cmp < 0 {
			fmt.Fprintf(s.out, "update available: %s (current: %s)\n", rel.TagName, s.currentVer)
			return errUpdateAvailable
		}
		fmt.Fprintf(s.out, "up to date (%s)\n", s.currentVer)
		return nil
	}

	if dev && !opts.Force {
		return fmt.Errorf("running a dev build; pass --force to overwrite with %s", rel.TagName)
	}
	if !dev && cmp == 0 && !opts.Force {
		fmt.Fprintf(s.out, "already at %s\n", rel.TagName)
		return nil
	}
	if !dev && cmp > 0 && !opts.Force {
		return fmt.Errorf("current %s is newer than %s; pass --force to downgrade", s.currentVer, rel.TagName)
	}

	binURL, shaURL, err := assetFor(rel, s.goos, s.goarch)
	if err != nil {
		return err
	}

	destDir := filepath.Dir(s.selfPath)
	tmpPath, wantHex, err := s.downloadAndVerify(ctx, binURL, shaURL, destDir)
	if err != nil {
		return err
	}

	if err := s.replaceBinary(tmpPath, s.selfPath, wantHex); err != nil {
		os.Remove(tmpPath)
		return err
	}
	fmt.Fprintf(s.out, "installed %s\n", rel.TagName)
	return nil
}

func (s *selfUpdate) get(ctx context.Context, url string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "flox-auto-activate-self-update/"+s.currentVer)
	return s.http.Do(req)
}

func (s *selfUpdate) latestRelease(ctx context.Context) (*release, error) {
	return s.fetchRelease(ctx, s.apiBase+"/repos/"+s.repo+"/releases/latest")
}

func (s *selfUpdate) releaseByTag(ctx context.Context, tag string) (*release, error) {
	return s.fetchRelease(ctx, s.apiBase+"/repos/"+s.repo+"/releases/tags/"+url.PathEscape(tag))
}

func (s *selfUpdate) fetchRelease(ctx context.Context, url string) (*release, error) {
	resp, err := s.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode release JSON: %w", err)
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("release JSON missing tag_name")
	}
	return &r, nil
}

func assetFor(r *release, goos, goarch string) (binURL, shaURL string, err error) {
	binName := fmt.Sprintf("flox-auto-activate-%s-%s", goos, goarch)
	shaName := binName + ".sha256"
	for _, a := range r.Assets {
		if a.Name == binName {
			binURL = a.DownloadURL
		}
		if a.Name == shaName {
			shaURL = a.DownloadURL
		}
	}
	if binURL == "" {
		return "", "", fmt.Errorf("no asset %q in release %s", binName, r.TagName)
	}
	if shaURL == "" {
		return "", "", fmt.Errorf("no checksum %q in release %s", shaName, r.TagName)
	}
	return binURL, shaURL, nil
}

func (s *selfUpdate) downloadAndVerify(ctx context.Context, binURL, shaURL, destDir string) (string, string, error) {
	shaResp, err := s.get(ctx, shaURL, "")
	if err != nil {
		return "", "", fmt.Errorf("fetch checksum: %w", err)
	}
	shaBytes, err := io.ReadAll(io.LimitReader(shaResp.Body, 4096))
	shaResp.Body.Close()
	if err != nil {
		return "", "", fmt.Errorf("read checksum: %w", err)
	}
	if shaResp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch checksum: %s", shaResp.Status)
	}
	fields := strings.Fields(string(shaBytes))
	if len(fields) == 0 {
		return "", "", fmt.Errorf("checksum response empty")
	}
	wantHex := strings.ToLower(fields[0])
	if len(wantHex) != 64 {
		return "", "", fmt.Errorf("checksum response malformed: %q", string(shaBytes))
	}

	binResp, err := s.get(ctx, binURL, "")
	if err != nil {
		return "", "", fmt.Errorf("fetch binary: %w", err)
	}
	defer binResp.Body.Close()
	if binResp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch binary: %s", binResp.Status)
	}

	tmpName := fmt.Sprintf("flox-auto-activate.new.%d", os.Getpid())
	tmpPath := filepath.Join(destDir, tmpName)
	// Mode 0o700: only the installing user can read/write the temp
	// binary, narrowing the TOCTOU window between download and rename.
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", "", fmt.Errorf("create %s: %w", tmpPath, err)
	}
	hasher := sha256.New()
	// Cap the download. If the body is exactly at the cap, that's
	// suspicious — the released binary is much smaller — so reject
	// it and let the user retry rather than silently truncating.
	limited := io.LimitReader(binResp.Body, maxBinaryBytes+1)
	n, err := io.Copy(io.MultiWriter(f, hasher), limited)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("download body: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", "", err
	}
	if n > maxBinaryBytes {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("download exceeds %d bytes; aborting", maxBinaryBytes)
	}
	gotHex := hex.EncodeToString(hasher.Sum(nil))
	if gotHex != wantHex {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("checksum mismatch: got %s, want %s", gotHex, wantHex)
	}
	return tmpPath, wantHex, nil
}

func (s *selfUpdate) replaceBinary(newPath, selfPath, wantHex string) error {
	// Re-hash from disk just before rename. Closes the TOCTOU window
	// between download-time hashing and the rename: if anything on
	// the filesystem tampered with the temp file in between (e.g. a
	// concurrent local process replaced it via the install dir), the
	// hash will diverge and we refuse to install it.
	if err := verifyFileHash(newPath, wantHex); err != nil {
		return err
	}
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}
	if err := os.Rename(newPath, selfPath); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot write to %s: %w (try installing to a user-owned path, or run with sudo)", filepath.Dir(selfPath), err)
		}
		return fmt.Errorf("replace %s: %w", selfPath, err)
	}
	return nil
}

func verifyFileHash(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("reopen for verify: %w", err)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("rehash: %w", err)
	}
	gotHex := hex.EncodeToString(hasher.Sum(nil))
	if gotHex != wantHex {
		return fmt.Errorf("checksum mismatch on re-verify: got %s, want %s (temp file may have been tampered with)", gotHex, wantHex)
	}
	return nil
}

// compareSemver returns -1/0/+1 for a<b / a==b / a>b. It accepts an
// optional leading 'v' and ignores any pre-release suffix after '-'.
// Returns an error if either side is not MAJOR.MINOR.PATCH of integers.
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", a, err)
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", b, err)
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseSemver(s string) ([3]int, error) {
	var out [3]int
	v := strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("expected MAJOR.MINOR.PATCH, got %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("component %d (%q) is not a non-negative integer", i, p)
		}
		out[i] = n
	}
	return out, nil
}
