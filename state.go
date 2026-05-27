package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Status int

const (
	StatusUnknown Status = iota
	StatusAllowed
	StatusDenied
)

type State struct {
	Version int      `json:"version"`
	Allowed []string `json:"allowed"`
	Denied  []string `json:"denied"`
	// Services records per-path service-start preference. A path present
	// here overrides the manifest default: true -> emit -s, false -> never
	// emit -s. Absent -> no override (manifest decides).
	Services map[string]bool `json:"services,omitempty"`

	path string
}

const stateVersion = 2

func defaultStatePath() (string, error) {
	if p := os.Getenv("FLOX_AUTO_ACTIVATE_STATE_FILE"); p != "" {
		return p, nil
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "flox-auto-activate", "state.json"), nil
}

func loadStateFrom(path string) (*State, error) {
	s := &State{Version: stateVersion, path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.path = path
	if s.Version == 0 {
		s.Version = stateVersion
	}
	return s, nil
}

func loadState() (*State, error) {
	path, err := defaultStatePath()
	if err != nil {
		return nil, err
	}
	return loadStateFrom(path)
}

func (s *State) save() error {
	if s.path == "" {
		return fmt.Errorf("state has no path")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (s *State) Status(path string) Status {
	p := filepath.Clean(path)
	for _, a := range s.Allowed {
		if a == p {
			return StatusAllowed
		}
	}
	for _, d := range s.Denied {
		if d == p {
			return StatusDenied
		}
	}
	return StatusUnknown
}

func (s *State) Allow(path string) {
	p := filepath.Clean(path)
	s.Denied = removeString(s.Denied, p)
	if !containsString(s.Allowed, p) {
		s.Allowed = append(s.Allowed, p)
	}
}

func (s *State) Deny(path string) {
	p := filepath.Clean(path)
	s.Allowed = removeString(s.Allowed, p)
	if !containsString(s.Denied, p) {
		s.Denied = append(s.Denied, p)
	}
}

// ServicesPref returns (enabled, set) for the given path.
// `set` is true iff the user has an explicit on/off override; if false,
// the caller should fall back to the manifest default (no `-s` flag).
func (s *State) ServicesPref(path string) (enabled, set bool) {
	if s.Services == nil {
		return false, false
	}
	v, ok := s.Services[filepath.Clean(path)]
	return v, ok
}

func (s *State) SetServices(path string, enabled bool) {
	if s.Services == nil {
		s.Services = make(map[string]bool)
	}
	s.Services[filepath.Clean(path)] = enabled
}

func (s *State) UnsetServices(path string) {
	if s.Services == nil {
		return
	}
	delete(s.Services, filepath.Clean(path))
	if len(s.Services) == 0 {
		s.Services = nil
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
