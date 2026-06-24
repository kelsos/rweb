package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/kelsos/rweb/internal/config"
)

// useTempState points the supervisor's state/log/lock dirs at a temp directory.
func useTempState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	xdg.Reload()
}

func TestSnapshotStates(t *testing.T) {
	useTempState(t)
	cfg := config.Default()

	// backend-only = docker (oneshot, excluded), django (http probe), huey (no probe).
	// huey is alive with no probe -> healthy; django is dead -> stopped.
	saveState(State{Profile: "backend-only", Instances: map[string]Instance{
		"huey":   {Name: "huey", PID: os.Getpid()},
		"django": {Name: "django", PID: 0},
	}})

	profile, snap := Snapshot(cfg)
	if profile != "backend-only" {
		t.Fatalf("profile = %q", profile)
	}
	byName := map[string]ServiceStatus{}
	var order []string
	for _, s := range snap {
		byName[s.Name] = s
		order = append(order, s.Name)
	}
	if _, ok := byName["docker"]; ok {
		t.Error("oneshot docker should not appear in snapshot")
	}
	if len(order) != 2 || order[0] != "django" || order[1] != "huey" {
		t.Fatalf("unexpected order %v", order)
	}
	if byName["huey"].State != "healthy" {
		t.Errorf("huey state = %q, want healthy", byName["huey"].State)
	}
	if byName["django"].State != "stopped" {
		t.Errorf("django state = %q, want stopped", byName["django"].State)
	}
}

func TestTrackedNamesOrder(t *testing.T) {
	useTempState(t)
	cfg := config.Default()
	saveState(State{Profile: "backend-only", Instances: map[string]Instance{
		"huey":   {Name: "huey", PID: 0},
		"django": {Name: "django", PID: 0},
		"extra":  {Name: "extra", PID: 0}, // leftover not in profile -> appended last
	}})

	got := TrackedNames(cfg)
	if len(got) != 3 || got[0] != "django" || got[1] != "huey" || got[2] != "extra" {
		t.Fatalf("TrackedNames = %v, want [django huey extra]", got)
	}
}

func TestStopServiceKillsAndForgets(t *testing.T) {
	useTempState(t)
	cfg := config.Default()

	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	pgid, _ := syscall.Getpgid(pid)
	// Reap the child once it's signalled so it doesn't linger as a zombie
	// (a zombie still answers Kill(pid, 0), unlike a detached supervisor child).
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	saveState(State{Profile: "backend-only", Instances: map[string]Instance{
		"huey": {Name: "huey", PID: pid, PGID: pgid},
	}})

	if err := StopService(cfg, "huey"); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	if _, ok := LoadState().Instances["huey"]; ok {
		t.Error("huey should be removed from state after stop")
	}

	// The process should be gone shortly after SIGTERM.
	dead := false
	for i := 0; i < 20; i++ {
		if !alive(pid) {
			dead = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !dead {
		t.Error("process still alive after StopService")
	}

	if err := StopService(cfg, "huey"); err == nil {
		t.Error("stopping an unknown service should error")
	}
}

func TestRotateLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "django.log")

	// No file yet: rotate is a no-op, no error.
	rotateLog(path)
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("rotate of a missing log should not create a backup")
	}

	// Non-empty file rotates to .1.
	if err := os.WriteFile(path, []byte("run1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotateLog(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("active log should be moved aside")
	}
	b, err := os.ReadFile(path + ".1")
	if err != nil || string(b) != "run1\n" {
		t.Errorf("previous run should be preserved in .1: %q %v", b, err)
	}

	// An empty file is left alone (nothing worth rotating).
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rotateLog(path)
	if _, err := os.Stat(path); err != nil {
		t.Error("empty active log should not be rotated")
	}
}

func TestResolveNest(t *testing.T) {
	cfg := config.Default()
	cfg.Repos.Nest = "/repos/nest"
	full, _ := Services(cfg, "full")        // has docker
	webOnly, _ := Services(cfg, "web-only") // no docker

	// Not requested: untouched.
	got, skipped, err := resolveNest(cfg, full, false)
	if err != nil || skipped || hasService(got, "nest") {
		t.Errorf("withNest=false should not attach nest (skipped=%v)", skipped)
	}

	// Requested + DB present: nest attached.
	got, skipped, err = resolveNest(cfg, full, true)
	if err != nil || skipped || !hasService(got, "nest") {
		t.Errorf("nest should attach to a docker-bearing profile (skipped=%v err=%v)", skipped, err)
	}

	// Requested but no DB in profile: skipped, not attached.
	got, skipped, err = resolveNest(cfg, webOnly, true)
	if err != nil || !skipped || hasService(got, "nest") {
		t.Errorf("nest needs the DB; web-only should skip it (skipped=%v)", skipped)
	}

	// Already present: no duplicate.
	withNest, _ := Services(cfg, "full")
	withNest = append(withNest, Service{Name: "nest"})
	got, _, _ = resolveNest(cfg, withNest, true)
	n := 0
	for _, s := range got {
		if s.Name == "nest" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("nest should not be duplicated, found %d", n)
	}
}
