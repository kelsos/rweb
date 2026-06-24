package proc

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kelsos/rweb/internal/config"
)

// depTarget is a repo whose dependencies rweb keeps in sync from its lockfile.
type depTarget struct {
	name string
	dir  string
	lock string // lockfile path, relative to dir
	cmd  string
	args []string // always a frozen/locked install — never mutates the lockfile
}

func depTargets(cfg *config.Config) []depTarget {
	// nest (rotki_nest) is a Rust service: `cargo run` resolves/builds deps on
	// demand from Cargo.lock, so it needs no separate frozen-install step here.
	return []depTarget{
		{"rotkehlchen_web", cfg.Repos.RotkehlchenWeb, "uv.lock", cfg.Tools.UV, []string{"sync", "--frozen"}},
		{"rotki_com", cfg.Repos.RotkiCom, "pnpm-lock.yaml", cfg.Tools.PNPM, []string{"install", "--frozen-lockfile"}},
	}
}

func depStampPath() string { return filepath.Join(stateDir(), "depsync.json") }

// DepLock is the presence of one dep-sync target's lockfile, for `rweb doctor`.
type DepLock struct {
	Name   string
	Path   string
	Exists bool
}

// DepLocks reports each configured dep-sync target's lockfile presence. Targets
// with no configured repo path are omitted.
func DepLocks(cfg *config.Config) []DepLock {
	var out []DepLock
	for _, t := range depTargets(cfg) {
		if t.dir == "" {
			continue
		}
		path := filepath.Join(t.dir, t.lock)
		_, err := os.Stat(path)
		out = append(out, DepLock{Name: t.name, Path: path, Exists: err == nil})
	}
	return out
}

// depDrift reports the lockfile's current mtime and whether a sync is needed. A
// missing lockfile is skipped (need=false); force always needs a sync.
func depDrift(lockPath string, stamps map[string]int64, force bool) (mod int64, need bool) {
	fi, err := os.Stat(lockPath)
	if err != nil {
		return 0, false
	}
	mod = fi.ModTime().UnixNano()
	if !force && stamps[lockPath] == mod {
		return mod, false
	}
	return mod, true
}

func loadDepStamps() map[string]int64 {
	stamps := map[string]int64{}
	b, err := os.ReadFile(depStampPath())
	if err != nil {
		return stamps
	}
	_ = json.Unmarshal(b, &stamps)
	return stamps
}

func saveDepStamps(stamps map[string]int64) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(stamps, "", "  ")
	return os.WriteFile(depStampPath(), b, 0o644)
}

// DepSync installs dependencies (frozen) for any repo whose lockfile changed
// since the last successful sync. With force, every configured repo is synced.
// Repos with no configured path or no lockfile are skipped.
func DepSync(cfg *config.Config, force bool) error {
	stamps := loadDepStamps()
	changed := false
	for _, t := range depTargets(cfg) {
		if t.dir == "" {
			continue
		}
		lockPath := filepath.Join(t.dir, t.lock)
		mod, need := depDrift(lockPath, stamps, force)
		if !need {
			continue
		}
		fmt.Printf("→ syncing deps: %s\n", t.name)
		c := exec.Command(t.cmd, t.args...)
		c.Dir = t.dir
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s deps: %w", t.name, err)
		}
		stamps[lockPath] = mod
		changed = true
	}
	if changed {
		return saveDepStamps(stamps)
	}
	return nil
}
