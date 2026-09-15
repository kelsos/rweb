// Package proc defines the orchestrated services and supervises them as detached
// child processes (own process groups, file-backed logs, a state file for
// reattach, and a flock against concurrent operations).
package proc

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/envutil"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/kelsos/rweb/internal/stackenv"
)

// Health describes how to probe a service for readiness.
type Health struct {
	HTTP     string   // GET this URL; <500 means healthy
	TCP      []string // each host:port must accept a connection
	Postgres *PGProbe // if set, pg_isready must pass inside the postgres container
}

// PGProbe runs `docker compose exec postgres pg_isready` to confirm the
// database is genuinely accepting connections (not merely that the port is
// open, which a TCP dial can see mid-initdb).
type PGProbe struct {
	Docker string // docker binary
	Dir    string // compose project directory (rotkehlchen-web)
}

// Service is one orchestrated process (or a one-shot bring-up step like docker).
type Service struct {
	Name     string
	Dir      string
	Cmd      string
	Args     []string
	BaseEnv  map[string]string // non-secret managed baseline (below repo files)
	EnvFiles []string          // repo .env files to load
	Env      map[string]string // extra non-secret env (applied after files, before secrets)
	Scope    string            // secret scope overlaid on top
	Health   Health
	Oneshot  bool // run to completion (not tracked as a long-running PID)
}

// Services returns the ordered services for a profile.
func Services(cfg *config.Config, profile string) ([]Service, error) {
	p, ok := cfg.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	out := make([]Service, 0, len(p.Services))
	for _, name := range p.Services {
		s, err := buildService(cfg, name)
		if err != nil {
			return nil, err
		}
		// Inject the non-secret managed baseline for the service's scope, so a
		// fresh checkout with no repo .env files still has its config: values
		// rweb derives from config first, then the user's [env.*] overrides.
		if s.Scope != "" {
			if base := stackenv.Baseline(cfg, s.Scope); len(base) > 0 {
				s.BaseEnv = base
			}
			// The active named-environment overlay sits above repo .env files,
			// so selecting an environment authoritatively switches its values.
			if ov := stackenv.Overlay(cfg, s.Scope); len(ov) > 0 {
				if s.Env == nil {
					s.Env = map[string]string{}
				}
				for k, v := range ov {
					s.Env[k] = v
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// ShortTmpdir pins TMPDIR to /tmp on macOS, where the default per-user temp dir
// is long enough to break Nuxt's vite-node unix socket. It returns nil on other
// platforms, so callers can merge it unconditionally.
//
// macOS caps sun_path at 104 bytes (Linux allows 108) and hands each user a
// TMPDIR like /var/folders/zn/n9fpvbnj3fn971b5s466fzd40000gn/T/ (~49 bytes).
// Nuxt appends a random dir plus nuxt-vite-node-<pid>-<ts>.sock (~61 more), so
// the path lands around 110 bytes and connect() fails with EINVAL. /tmp is 4
// bytes and leaves ample headroom. Linux has no such problem, so leave its env
// untouched rather than overriding a TMPDIR the user may have set on purpose.
//
// Anything that starts a Nuxt dev server needs this: the nuxt service itself,
// and the Playwright suites, which spin one up of their own.
func ShortTmpdir() map[string]string { return shortTmpdirFor(runtime.GOOS) }

func shortTmpdirFor(goos string) map[string]string {
	if goos != "darwin" {
		return nil
	}
	return map[string]string{"TMPDIR": "/tmp"}
}

func buildService(cfg *config.Config, name string) (Service, error) {
	rw := cfg.Repos.RotkehlchenWeb
	rc := cfg.Repos.RotkiCom
	rwEnv := []string{filepath.Join(rw, "localtest_env"), filepath.Join(rw, ".env")}

	switch name {
	case "docker":
		return Service{
			Name: "docker", Dir: rw, Cmd: cfg.Tools.Docker,
			Args: []string{"compose", "up", "-d"}, EnvFiles: rwEnv, Scope: secrets.ScopeShared,
			Oneshot: true,
			Health: Health{
				TCP: []string{
					fmt.Sprintf("localhost:%d", cfg.Ports.Redis),
				},
				// Postgres needs a real readiness probe: the mapped port can
				// accept a TCP connection while the server is still running
				// initdb, which would let migrations fire too early.
				Postgres: &PGProbe{Docker: cfg.Tools.Docker, Dir: rw},
			},
		}, nil
	case "django":
		return Service{
			Name: "django", Dir: rw, Cmd: cfg.Tools.UV,
			Args:     []string{"run", "manage.py", "runserver", fmt.Sprintf("0.0.0.0:%d", cfg.Ports.Django)},
			EnvFiles: rwEnv, Scope: secrets.ScopeDjango,
			// Unbuffer Python stdout/stderr so log lines reach the file promptly
			// (Python block-buffers when not writing to a TTY, and rweb captures
			// to a file). --runserver also already auto-reloads.
			Env:    map[string]string{"PYTHONUNBUFFERED": "1"},
			Health: Health{HTTP: fmt.Sprintf("http://localhost:%d/", cfg.Ports.Django)},
		}, nil
	case "huey":
		return Service{
			Name: "huey", Dir: rw, Cmd: cfg.Tools.UV,
			Args: []string{"run", "manage.py", "run_huey"}, EnvFiles: rwEnv, Scope: secrets.ScopeDjango,
			Env: map[string]string{"PYTHONUNBUFFERED": "1"},
		}, nil
	case "go-dev":
		return Service{
			Name: "go-dev", Dir: rc, Cmd: cfg.Tools.Make,
			Args:     []string{"dev-go"},
			EnvFiles: []string{filepath.Join(rc, "backend", ".env")}, Scope: secrets.ScopeGoBackend,
			Health: Health{HTTP: fmt.Sprintf("http://localhost:%d/health", cfg.Ports.GoDev)},
		}, nil
	case "nuxt":
		return Service{
			Name: "nuxt", Dir: rc, Cmd: cfg.Tools.Make,
			Args:     []string{"dev-web"},
			EnvFiles: []string{filepath.Join(rc, "packages", "website", ".env")}, Scope: secrets.ScopeNuxt,
			Env:    ShortTmpdir(),
			Health: Health{HTTP: fmt.Sprintf("http://localhost:%d", cfg.Ports.Nuxt)},
		}, nil
	case "nest":
		if cfg.Repos.Nest == "" {
			return Service{}, fmt.Errorf("nest requested but repos.nest is not configured")
		}
		// rotki_nest is a Rust/axum service: `cargo run`, config from its own
		// committed configuration.yaml (incl. DB creds), with NEST_PORT to align
		// on rweb's port. SQLX_OFFLINE uses the repo's .sqlx cache so the build
		// needs no live DB / DATABASE_URL.
		return Service{
			Name: "nest", Dir: cfg.Repos.Nest, Cmd: cfg.Tools.Cargo,
			Args: []string{"run"},
			Env: map[string]string{
				"NEST_PORT":    fmt.Sprintf("%d", cfg.Ports.Nest),
				"SQLX_OFFLINE": "true",
			},
			Health: Health{TCP: []string{fmt.Sprintf("localhost:%d", cfg.Ports.Nest)}},
		}, nil
	case "build-web":
		// Same env as the nuxt dev server: NUXT_PUBLIC_* values are baked at
		// generate time, so the build needs the nuxt scope + baseline too.
		return Service{
			Name: "build-web", Dir: rc, Cmd: cfg.Tools.Make,
			Args:     []string{"build-web"},
			EnvFiles: []string{filepath.Join(rc, "packages", "website", ".env")}, Scope: secrets.ScopeNuxt,
			Oneshot: true,
		}, nil
	case "go-serve":
		return Service{
			Name: "go-serve", Dir: rc, Cmd: cfg.Tools.Make,
			Args:  []string{"-C", "backend", "run"},
			Scope: secrets.ScopeGoBackend,
			Env: map[string]string{
				"STATIC_DIR":     filepath.Join(rc, cfg.Review.StaticDir),
				"PORT":           fmt.Sprintf("%d", cfg.Review.Port),
				"PROXY_DOMAIN":   fmt.Sprintf("localhost:%d", cfg.Ports.Django),
				"PROXY_INSECURE": "true",
				"DEV_MODE":       "false",
			},
			Health: Health{HTTP: fmt.Sprintf("http://localhost:%d/health", cfg.Review.Port)},
		}, nil
	default:
		return Service{}, fmt.Errorf("unknown service %q", name)
	}
}

// SecretSource provides a service's secret env for a scope.
type SecretSource = stackenv.SecretSource

// BuildEnv composes a service's environment: os.Environ + base + repo env files,
// then the service's own Env (incl. the named-env overlay), then the secret
// scope overlaid last (the central store wins, env secrets over base secrets).
func BuildEnv(s Service, src SecretSource) ([]string, error) {
	overlays := []map[string]string{}
	if s.Env != nil {
		overlays = append(overlays, s.Env)
	}
	if s.Scope != "" {
		m, err := src.EnvFor(s.Scope)
		if err != nil {
			return nil, err
		}
		overlays = append(overlays, m)
	}
	return envutil.ComposeWithBase(s.BaseEnv, s.EnvFiles, overlays...), nil
}
