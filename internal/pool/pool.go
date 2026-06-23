// Package pool implements dcon's warm-VM pool: a set of pre-booted, single-use
// Apple-container microVMs kept idle so that a `dcon run` can skip the ~650 ms
// VM cold boot and instead `exec` the workload into an already-running VM
// (~70 ms on this hardware — faster than a shared-VM engine like OrbStack).
//
// The key property is that pooling does NOT weaken isolation: every pool member
// is a fresh VM booted from the image and handed out exactly once, then
// destroyed. The boot cost is simply paid ahead of time, in the background,
// instead of on the user's critical path.
//
// dcon has no daemon of its own, so the pool's bookkeeping lives in a small
// JSON file guarded by an advisory file lock; the warm VMs themselves are owned
// by Apple's apiserver (which persists across dcon invocations). The state file
// only ever lists AVAILABLE members — claiming one removes it from the file, so
// two concurrent `dcon run`s can never hand out the same VM.
package pool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dcon/internal/runtime"
)

// Label keys stamped on every pool member so leaked VMs (from a crashed dcon
// process) can be found and reaped by image, independent of the state file.
const (
	LabelPool  = "dcon.pool"
	LabelImage = "dcon.pool.image"

	// keepAlive blocks PID-of-the-workload's sibling forever (~68 years) so the
	// member VM stays up until dcon explicitly destroys it. Relies on a `sleep`
	// binary in the image (coreutils/busybox) — virtually every base image ships
	// one; images that don't simply fall back to the cold path.
	keepAlive = "2147483647"
)

// Member is one available, pre-booted VM waiting to be claimed.
type Member struct {
	ID       string `json:"id"`       // backend container ID
	Image    string `json:"image"`    // normalized image ref it was booted from
	BootedAt int64  `json:"bootedAt"` // unix seconds
}

type state struct {
	Members []Member `json:"members"`
}

// dir returns dcon's private state directory (created on demand).
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
	return filepath.Join(d, "pool.json"), nil
}

// withLock runs fn while holding an exclusive advisory lock on the state file's
// lock companion, serializing read-modify-write across concurrent dcon
// processes. The state is loaded, passed to fn (which may mutate it), and saved
// iff fn returns nil.
func withLock(fn func(s *state) error) error {
	d, err := dir()
	if err != nil {
		return err
	}
	lockFile := filepath.Join(d, "pool.lock")
	lf, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o644)
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
		// Corrupt state file: start clean rather than wedging every run.
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

// NormalizeRef canonicalizes an image reference for pool matching so that
// `alpine`, `alpine:latest`, and `docker.io/library/alpine:latest` all key the
// same pool. It only adds an implicit `:latest`; it deliberately does not
// rewrite registry hosts (that is the backend's job) — it just has to be a
// stable, collision-free key shared by warm and run.
func NormalizeRef(image string) string {
	ref := image
	// A digest pin is already fully qualified.
	if strings.Contains(ref, "@") {
		return ref
	}
	// Find the final path segment to decide whether a tag is present (a colon in
	// an earlier segment is a registry port, not a tag).
	slash := strings.LastIndex(ref, "/")
	last := ref[slash+1:]
	if !strings.Contains(last, ":") {
		ref += ":latest"
	}
	return ref
}

// TargetDepth is how many warm members dcon tries to keep per image when
// auto-warming is enabled. Overridable via DCON_WARM_DEPTH (clamped to 1..8).
func TargetDepth() int {
	n := 1
	if v := os.Getenv("DCON_WARM_DEPTH"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			n = p
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

// TTL is how long an idle warm member may sit before opportunistic reaping in
// auto mode. Overridable via DCON_WARM_TTL (seconds); 0 disables reaping.
func TTL() time.Duration {
	secs := 600 // 10 minutes
	if v := os.Getenv("DCON_WARM_TTL"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 {
			secs = p
		}
	}
	return time.Duration(secs) * time.Second
}

// ReapStale retires warm members that have sat idle past the TTL so a forgotten
// pool can't pin memory indefinitely. Gated on auto mode: a manually seeded
// pool is the user's to manage and is never reaped out from under them. The
// teardown is detached, keeping callers off the critical path.
func ReapStale() {
	if !AutoEnabled() {
		return
	}
	ttl := TTL()
	if ttl <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(ttl.Seconds())
	for _, m := range List() {
		if m.BootedAt < cutoff {
			forget(m.ID)
			DestroyAsync(m.ID)
		}
	}
}

// AutoEnabled reports whether dcon should self-prime the pool after eligible
// runs. Off by default to preserve the low idle-memory footprint; opt in with
// DCON_WARM=auto (or =1/on/true).
func AutoEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DCON_WARM"))) {
	case "auto", "1", "on", "true", "yes":
		return true
	}
	return false
}

// Disabled reports whether pooling is hard-off (DCON_WARM=off), in which case
// even an explicitly seeded pool is ignored. Useful for the cold-path
// benchmark and for users who want guaranteed fresh-boot semantics.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DCON_WARM"))) {
	case "off", "0", "no", "false":
		return true
	}
	return false
}

// Claim atomically removes and returns an available member for image. ok is
// false if the pool has none. The returned member is now owned by the caller,
// which MUST eventually Destroy it (the VM is no longer tracked in state).
func Claim(image string) (Member, bool) {
	norm := NormalizeRef(image)
	var claimed Member
	var ok bool
	_ = withLock(func(s *state) error {
		for i, m := range s.Members {
			if m.Image == norm {
				claimed = m
				s.Members = append(s.Members[:i], s.Members[i+1:]...)
				ok = true
				return nil
			}
		}
		return nil
	})
	return claimed, ok
}

// Add records a freshly booted member as available.
func Add(m Member) error {
	return withLock(func(s *state) error {
		s.Members = append(s.Members, m)
		return nil
	})
}

// AvailableDepth returns how many available members the state currently tracks
// for image (does not verify liveness — cheap, used to decide replenishment).
func AvailableDepth(image string) int {
	norm := NormalizeRef(image)
	n := 0
	_ = withLock(func(s *state) error {
		for _, m := range s.Members {
			if m.Image == norm {
				n++
			}
		}
		return nil
	})
	return n
}

// List returns every tracked available member.
func List() []Member {
	var out []Member
	_ = withLock(func(s *state) error {
		out = append(out, s.Members...)
		return nil
	})
	return out
}

// forget drops member id from the state file (used when a claimed member turns
// out to be dead, or during prune).
func forget(id string) {
	_ = withLock(func(s *state) error {
		kept := s.Members[:0]
		for _, m := range s.Members {
			if m.ID != id {
				kept = append(kept, m)
			}
		}
		s.Members = kept
		return nil
	})
}

// Boot synchronously cold-boots one warm member for image and records it.
// This is the slow operation (it pays the VM boot) and is meant to run in the
// background or from an explicit `dcon warm`.
func Boot(image string) (Member, error) {
	norm := NormalizeRef(image)
	out, err := runtime.CaptureSilent(
		"run", "-d",
		"--label", LabelPool+"=1",
		"--label", LabelImage+"="+norm,
		image, "sleep", keepAlive,
	)
	if err != nil {
		return Member{}, err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return Member{}, fmt.Errorf("backend returned no container ID for warm boot of %s", image)
	}
	m := Member{ID: id, Image: norm, BootedAt: time.Now().Unix()}
	if err := Add(m); err != nil {
		// Don't leak the VM if we couldn't record it.
		_ = Destroy(id)
		return Member{}, err
	}
	return m, nil
}

// Replenish tops the pool for image back up to TargetDepth by spawning a
// detached background `dcon warm` that outlives the current (short-lived)
// process. It is gated on auto mode (DCON_WARM=auto): a manually seeded pool
// drains predictably as it is consumed, while auto mode sustains warm depth.
func Replenish(image string) {
	if !AutoEnabled() {
		return
	}
	need := TargetDepth() - AvailableDepth(image)
	if need <= 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(exe, "warm", image, "-n", strconv.Itoa(need), "--quiet")
	// Detach completely: no stdio, own session, not waited on.
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil && c.Process != nil {
		_ = c.Process.Release()
	}
}

// Destroy force-removes a member VM (best effort, synchronous).
func Destroy(id string) error {
	// `rm --force` stops if running, then deletes, in one backend call.
	_, err := runtime.CaptureSilent("rm", "--force", id)
	return err
}

// DestroyAsync retires a member VM in a detached background process so it never
// sits on the user's critical path: a `dcon run` served from the pool returns
// as soon as the workload's exec completes, and the (~100 ms) VM teardown
// happens afterward, outliving this short-lived process.
func DestroyAsync(id string) {
	c := exec.Command(runtime.Bin(), "rm", "--force", id)
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil && c.Process != nil {
		_ = c.Process.Release()
	}
}

// IsRunning reports whether a container id is currently running, per the
// backend. Used only on the exec error path to decide transparent fallback.
func IsRunning(id string) bool {
	var states []struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := runtime.CaptureJSON(&states, "inspect", id); err != nil {
		return false
	}
	for _, s := range states {
		if s.Status.State == "running" {
			return true
		}
	}
	return false
}

// PruneOrphans force-removes every backend container labeled as a pool member
// and clears the state file. If image is non-empty, only that image's members
// are removed. Returns the number of VMs torn down.
func PruneOrphans(image string) (int, error) {
	var rows []struct {
		ID            string `json:"id"`
		Configuration struct {
			Labels map[string]string `json:"labels"`
		} `json:"configuration"`
	}
	if err := runtime.CaptureJSON(&rows, "ls", "--all", "--format", "json"); err != nil {
		return 0, err
	}
	norm := ""
	if image != "" {
		norm = NormalizeRef(image)
	}
	n := 0
	for _, r := range rows {
		if r.Configuration.Labels[LabelPool] != "1" {
			continue
		}
		if norm != "" && r.Configuration.Labels[LabelImage] != norm {
			continue
		}
		if Destroy(r.ID) == nil {
			n++
		}
		forget(r.ID)
	}
	// Drop any stale state entries for this image (members whose VM already
	// vanished) so `warm ls` reflects reality.
	_ = withLock(func(s *state) error {
		kept := s.Members[:0]
		for _, m := range s.Members {
			if norm == "" || m.Image == norm {
				continue
			}
			kept = append(kept, m)
		}
		s.Members = kept
		return nil
	})
	return n, nil
}
