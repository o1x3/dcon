package machine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// State persisted for machines. The set of machines themselves is NOT stored
// here — it is derived from the backend (containers carrying LabelMachine), so
// it can never drift from reality. Only the user's chosen default machine, which
// has no backend representation, lives in this file.
type state struct {
	Default string `json:"default"`
}

// dir returns dcon's private state directory (created on demand), matching the
// warm pool so both live under the same app config dir.
func dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	d := filepath.Join(base, "dcon")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

func statePath() (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "machines.json"), nil
}

// withLock runs fn while holding an exclusive advisory lock, serializing the
// read-modify-write of the default pointer across concurrent dcon processes.
func withLock(fn func(s *state) error) error {
	d, err := dir()
	if err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(d, "machines.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	s, err := load()
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return save(s)
}

func load() (*state, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &state{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s state
	if len(b) == 0 {
		return &s, nil
	}
	if err := json.Unmarshal(b, &s); err != nil {
		// Corrupt state: start clean rather than wedging every machine command.
		return &state{}, nil
	}
	return &s, nil
}

func save(s *state) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Default returns the current default machine name, or "" if none is set.
func Default() string {
	s, err := load()
	if err != nil {
		return ""
	}
	return s.Default
}

// SetDefault records name as the default machine.
func SetDefault(name string) error {
	return withLock(func(s *state) error {
		s.Default = name
		return nil
	})
}

// SetDefaultIfUnset makes name the default only when no default is set, so the
// first machine created becomes the default automatically without clobbering a
// later explicit choice.
func SetDefaultIfUnset(name string) error {
	return withLock(func(s *state) error {
		if s.Default == "" {
			s.Default = name
		}
		return nil
	})
}

// ClearDefaultIf clears the default pointer iff it currently names this machine
// (used when that machine is removed, so the pointer never dangles).
func ClearDefaultIf(name string) error {
	return withLock(func(s *state) error {
		if s.Default == name {
			s.Default = ""
		}
		return nil
	})
}
