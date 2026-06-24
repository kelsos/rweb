package proc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/adrg/xdg"
	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/db"
	"github.com/kelsos/rweb/internal/secrets"
)

// Instance is a tracked long-running child process.
type Instance struct {
	Name    string `json:"name"`
	PID     int    `json:"pid"`
	PGID    int    `json:"pgid"`
	Log     string `json:"log"`
	Started string `json:"started"`
}

// State is the persisted supervisor state (enables reattach across rweb runs).
type State struct {
	Profile   string              `json:"profile"`
	Instances map[string]Instance `json:"instances"`
}

func stateDir() string  { return filepath.Join(xdg.StateHome, "rweb") }
func statePath() string { return filepath.Join(stateDir(), "state.json") }
func logDir() string    { return filepath.Join(stateDir(), "logs") }
func lockPath() string  { return filepath.Join(stateDir(), "rweb.lock") }

// LoadState reads the persisted state (empty if none).
func LoadState() State {
	st := State{Instances: map[string]Instance{}}
	b, err := os.ReadFile(statePath())
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	if st.Instances == nil {
		st.Instances = map[string]Instance{}
	}
	return st
}

func saveState(st State) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(statePath(), b, 0o644)
}

func acquireLock() (*os.File, error) {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another rweb operation is in progress")
	}
	return f, nil
}

func releaseLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// UpOpts controls Up behaviour.
type UpOpts struct {
	Migrate  bool
	WithNest bool
	SyncDeps bool
}

// Up starts a profile in dependency order, waiting for each service's health
// probe before continuing. Already-running services are reattached, not restarted.
func Up(cfg *config.Config, st *secrets.Store, profile string, opts UpOpts) error {
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	services, err := Services(cfg, profile)
	if err != nil {
		return err
	}
	services, nestSkipped, err := resolveNest(cfg, services, opts.WithNest)
	if err != nil {
		return err
	}
	if nestSkipped {
		fmt.Printf("• nest configured but profile %q has no database; skipping nest\n", profile)
	}
	djangoPresent := hasService(services, "django")

	if opts.SyncDeps {
		if err := DepSync(cfg, false); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(logDir(), 0o755); err != nil {
		return err
	}
	state := LoadState()
	state.Profile = profile

	for _, s := range services {
		env, err := BuildEnv(s, secretSource(cfg, st))
		if err != nil {
			return err
		}

		switch {
		case s.Oneshot:
			fmt.Printf("→ %s\n", s.Name)
			if err := runOneshot(s, env); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		case stateAlive(state, s.Name):
			fmt.Printf("• %s already running (pid %d)\n", s.Name, state.Instances[s.Name].PID)
		default:
			fmt.Printf("→ starting %s\n", s.Name)
			inst, err := startProc(s, env)
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			state.Instances[s.Name] = inst
			if err := saveState(state); err != nil {
				return err
			}
		}

		var aliveFn func() bool
		if !s.Oneshot {
			pid := state.Instances[s.Name].PID
			aliveFn = func() bool { return alive(pid) }
		}
		if err := waitHealthy(s.Name, s.Health, 90*time.Second, aliveFn); err != nil {
			if tail := tailString(s.Name, 30); tail != "" {
				fmt.Fprintln(os.Stderr, tail)
			}
			return err
		}
		fmt.Printf("✓ %s healthy\n", s.Name)

		// Once infra is up, migrate before the app servers start.
		if s.Name == "docker" && opts.Migrate && djangoPresent {
			fmt.Println("→ migrating database")
			if err := db.Migrate(cfg, st); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			fmt.Println("✓ migrations applied")
		}
	}

	fmt.Printf("\nStack %q is up.\n", profile)
	return nil
}

func stateAlive(state State, name string) bool {
	inst, ok := state.Instances[name]
	return ok && alive(inst.PID)
}

func startProc(s Service, env []string) (Instance, error) {
	if err := os.MkdirAll(logDir(), 0o755); err != nil {
		return Instance{}, err
	}
	logPath := filepath.Join(logDir(), s.Name+".log")
	rotateLog(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Instance{}, err
	}
	defer f.Close()

	c := exec.Command(s.Cmd, s.Args...)
	c.Dir = s.Dir
	c.Env = env
	c.Stdout = f
	c.Stderr = f
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return Instance{}, err
	}
	pid := c.Process.Pid
	pgid, _ := syscall.Getpgid(pid)
	_ = c.Process.Release() // detach: child survives rweb exiting
	return Instance{Name: s.Name, PID: pid, PGID: pgid, Log: logPath, Started: time.Now().Format(time.RFC3339)}, nil
}

// resolveNest appends the nest service to a profile's services when it's
// requested and not already present, but only if the profile runs the Postgres
// nest depends on. It returns the (possibly extended) services and whether nest
// was requested yet skipped (because the profile has no database).
func resolveNest(cfg *config.Config, services []Service, withNest bool) ([]Service, bool, error) {
	if !withNest || hasService(services, "nest") {
		return services, false, nil
	}
	if !hasService(services, "docker") {
		return services, true, nil
	}
	ns, err := buildService(cfg, "nest")
	if err != nil {
		return services, false, err
	}
	return append(services, ns), false, nil
}

// rotateLog moves an existing non-empty log aside to <path>.1 before a service
// starts, so each run begins a fresh log and the previous run is preserved as a
// single backup. Children write straight to the file descriptor (rweb detaches),
// so per-run rotation at start is the bound on growth; the active file holds just
// the current run. Best-effort: a rotation failure must not block the start.
func rotateLog(path string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		_ = os.Rename(path, path+".1")
	}
}

func runOneshot(s Service, env []string) error {
	c := exec.Command(s.Cmd, s.Args...)
	c.Dir = s.Dir
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func stopInstance(inst Instance) {
	signalTree(inst, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		if !alive(inst.PID) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	signalTree(inst, syscall.SIGKILL)
}

func signalTree(inst Instance, sig syscall.Signal) {
	if inst.PGID > 0 {
		_ = syscall.Kill(-inst.PGID, sig)
		return
	}
	_ = syscall.Kill(inst.PID, sig)
}

// Down stops rweb-managed long-running services (docker infra is left running).
// Down stops the rweb-managed services. When stopDocker is set it also tears
// down the docker infra (`docker compose down`); otherwise that is left running.
func Down(cfg *config.Config, stopDocker bool) error {
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	state := LoadState()
	if len(state.Instances) == 0 {
		fmt.Println("no managed services running")
	} else {
		order := serviceOrder(cfg, state.Profile)
		for i := len(order) - 1; i >= 0; i-- {
			name := order[i]
			if inst, ok := state.Instances[name]; ok {
				stopInstance(inst)
				delete(state.Instances, name)
				_ = saveState(state)
				fmt.Printf("✓ stopped %s\n", name)
			}
		}
		for name, inst := range state.Instances { // leftovers not in profile order
			stopInstance(inst)
			delete(state.Instances, name)
			fmt.Printf("✓ stopped %s\n", name)
		}
		_ = saveState(state)
	}

	if stopDocker {
		fmt.Println("\nStopping docker infra…")
		if err := StopDocker(cfg); err != nil {
			return fmt.Errorf("stop docker: %w", err)
		}
		fmt.Println("✓ docker infra stopped")
		return nil
	}
	fmt.Println("\nDocker infra left running (use `rweb down --all` or `rweb docker down` to stop it).")
	return nil
}

// Restart stops (if running) and starts a single service.
func Restart(cfg *config.Config, st *secrets.Store, name string) error {
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	state := LoadState()
	if inst, ok := state.Instances[name]; ok {
		stopInstance(inst)
		delete(state.Instances, name)
		_ = saveState(state)
	}
	s, err := buildService(cfg, name)
	if err != nil {
		return err
	}
	env, err := BuildEnv(s, secretSource(cfg, st))
	if err != nil {
		return err
	}
	if s.Oneshot {
		return runOneshot(s, env)
	}
	inst, err := startProc(s, env)
	if err != nil {
		return err
	}
	state.Instances[name] = inst
	if err := saveState(state); err != nil {
		return err
	}
	return waitHealthy(name, s.Health, 90*time.Second, func() bool { return alive(inst.PID) })
}

// ServiceStatus is a point-in-time view of one tracked service, shared by the
// CLI status table and the TUI dashboard.
type ServiceStatus struct {
	Name  string
	PID   int
	State string // "stopped" | "starting" | "healthy"
	Log   string
}

// Snapshot returns the active profile and the status of its (non-oneshot)
// services, plus any tracked leftovers not in the profile. Returns an empty
// profile and nil slice when nothing has been started.
func Snapshot(cfg *config.Config) (string, []ServiceStatus) {
	state := LoadState()
	healthByName := map[string]Health{}
	var order []string
	if services, err := Services(cfg, state.Profile); err == nil {
		for _, s := range services {
			if s.Oneshot {
				continue
			}
			healthByName[s.Name] = s.Health
			order = append(order, s.Name)
		}
	}

	var out []ServiceStatus
	seen := map[string]bool{}
	add := func(name string) {
		ss := ServiceStatus{Name: name, State: "stopped"}
		if inst, ok := state.Instances[name]; ok {
			ss.PID, ss.Log = inst.PID, inst.Log
			if alive(inst.PID) {
				if checkHealth(healthByName[name]) == nil {
					ss.State = "healthy"
				} else {
					ss.State = "starting"
				}
			}
		}
		out = append(out, ss)
		seen[name] = true
	}
	for _, name := range order {
		add(name)
	}
	for name := range state.Instances {
		if !seen[name] {
			add(name)
		}
	}
	return state.Profile, out
}

// Status prints a table of tracked services with liveness + health.
func Status(cfg *config.Config) error {
	if len(LoadState().Instances) == 0 {
		fmt.Println("no services running")
		return nil
	}
	profile, snap := Snapshot(cfg)
	fmt.Printf("profile: %s\n\n", profile)
	fmt.Printf("%-10s %-8s %-9s %s\n", "SERVICE", "PID", "STATE", "LOG")
	for _, s := range snap {
		fmt.Printf("%-10s %-8d %-9s %s\n", s.Name, s.PID, s.State, s.Log)
	}
	return nil
}

// StopService stops a single tracked long-running service.
func StopService(cfg *config.Config, name string) error {
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	state := LoadState()
	inst, ok := state.Instances[name]
	if !ok {
		return fmt.Errorf("%q is not running", name)
	}
	stopInstance(inst)
	delete(state.Instances, name)
	return saveState(state)
}

// Tail prints a service's log file, optionally following it.
func Tail(name string, follow bool) error {
	f, err := os.Open(filepath.Join(logDir(), name+".log"))
	if err != nil {
		return fmt.Errorf("no logs for %q", name)
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	for {
		time.Sleep(400 * time.Millisecond)
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return err
		}
	}
}

// StartDocker brings up only the docker infra and waits for it to be ready.
func StartDocker(cfg *config.Config, st *secrets.Store) error {
	s, err := buildService(cfg, "docker")
	if err != nil {
		return err
	}
	env, err := BuildEnv(s, secretSource(cfg, st))
	if err != nil {
		return err
	}
	if err := runOneshot(s, env); err != nil {
		return err
	}
	return waitHealthy(s.Name, s.Health, 60*time.Second, nil)
}

// StopDocker stops the docker infra (`docker compose down`).
func StopDocker(cfg *config.Config) error {
	if cfg.Repos.RotkehlchenWeb == "" {
		return fmt.Errorf("repos.rotkehlchen_web not set in config")
	}
	c := exec.Command(cfg.Tools.Docker, "compose", "down")
	c.Dir = cfg.Repos.RotkehlchenWeb
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func tailString(name string, lines int) string {
	b, err := os.ReadFile(filepath.Join(logDir(), name+".log"))
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return name + " log tail:\n    " + strings.Join(all, "\n    ")
}

func hasService(ss []Service, name string) bool {
	for _, s := range ss {
		if s.Name == name {
			return true
		}
	}
	return false
}

// serviceOrder returns the tracked (non-oneshot) service names in profile order.
func serviceOrder(cfg *config.Config, profile string) []string {
	services, err := Services(cfg, profile)
	if err != nil {
		return nil
	}
	var order []string
	for _, s := range services {
		if !s.Oneshot {
			order = append(order, s.Name)
		}
	}
	return order
}
