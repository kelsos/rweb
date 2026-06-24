// Package proc defines the orchestrated services and supervises them as detached
// child processes (own process groups, file-backed logs, a state file for
// reattach, and a flock against concurrent operations).
package proc

import (
	"fmt"
	"path/filepath"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/envutil"
	"github.com/kelsos/rweb/internal/secrets"
)

// Health describes how to probe a service for readiness.
type Health struct {
	HTTP string   // GET this URL; <500 means healthy
	TCP  []string // each host:port must accept a connection
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
			base := derivedEnv(cfg, s.Scope)
			for k, v := range cfg.Env[s.Scope] {
				base[k] = v
			}
			if len(base) > 0 {
				s.BaseEnv = base
			}
			// The active named-environment overlay sits above repo .env files,
			// so selecting an environment authoritatively switches its values.
			if ov := envOverlay(cfg, s.Scope); len(ov) > 0 {
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

// Google's universal reCAPTCHA v2 test keys — public, always-pass values meant
// for development. Shipped as dev defaults; DJANGO_DEBUG=True silences the
// test-key warning. A real key set in the secret store overrides them.
const (
	recaptchaTestSiteKey   = "6LeIxAcTAAAAAJcZVRqyHh71UMIEGNQ_MXjiZKhI"
	recaptchaTestSecretKey = "6LeIxAcTAAAAAGG-vFI1TnRWxMZNFuojJ4WifJWe"
)

// derivedEnv returns non-secret env values rweb computes from config for a
// scope, so they need not be set by hand or duplicated in secrets. These sit at
// the managed-baseline level, so repo .env files and explicit [env.*] entries
// still override them. Several Django settings hard-fail (ValueError) when
// unset, so we supply dev-safe defaults here to keep the "a fresh checkout with
// no .env files still boots" promise.
func derivedEnv(cfg *config.Config, scope string) map[string]string {
	switch scope {
	case secrets.ScopeShared:
		return map[string]string{"REDIS_HOST": "localhost"}
	case secrets.ScopeDjango:
		return map[string]string{
			"DB_HOST": cfg.DB.Host,
			"DB_PORT": fmt.Sprintf("%d", cfg.DB.Port),
			"DB_NAME": cfg.DB.Name,
			"DB_USER": cfg.DB.User,
			"DB_TYPE": "postgres",
			// Required-but-defaultable settings Django raises ValueError without.
			"DJANGO_DEBUG":            "True",
			"DOMAIN":                  "localhost",
			"UPLOADED_BACKUPS_FOLDER": "data/backups",
			"BRAINTREE_PRODUCTION":    "False",
			// Email → mailpit (SMTP :1025), viewable via `rweb open mailpit`.
			"EMAIL_TYPE": "MAILHOG",
			"EMAIL_HOST": "localhost",
			"EMAIL_PORT": "1025",
			// reCAPTCHA dev test keys (overridden by a real secret if set).
			"RECAPTCHA_PUBLIC_KEY":  recaptchaTestSiteKey,
			"RECAPTCHA_PRIVATE_KEY": recaptchaTestSecretKey,
		}
	case secrets.ScopeNuxt:
		return map[string]string{
			"NUXT_PUBLIC_RECAPTCHA_SITE_KEY": recaptchaTestSiteKey,
		}
	}
	return map[string]string{}
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
			Health: Health{TCP: []string{
				fmt.Sprintf("localhost:%d", cfg.Ports.Postgres),
				fmt.Sprintf("localhost:%d", cfg.Ports.Redis),
			}},
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

// envOverlay returns the active named environment's non-secret overlay for a
// scope (nil for the default/base env). It sits above repo .env files.
func envOverlay(cfg *config.Config, scope string) map[string]string {
	if cfg.ActiveEnv == "" || cfg.ActiveEnv == config.DefaultEnv {
		return nil
	}
	p, ok := cfg.Environments[cfg.ActiveEnv]
	if !ok {
		return nil
	}
	return p.Env[scope]
}

// SecretSource provides a service's secret env for a scope. Both *secrets.Store
// and the base+overlay merge below satisfy it.
type SecretSource interface {
	EnvFor(scope string) (map[string]string, error)
}

// multiSource merges a base secret store with a named environment's overlay
// store; the overlay wins on conflicts.
type multiSource struct{ base, env SecretSource }

func (m multiSource) EnvFor(scope string) (map[string]string, error) {
	out := map[string]string{}
	b, err := m.base.EnvFor(scope)
	if err != nil {
		return nil, err
	}
	for k, v := range b {
		out[k] = v
	}
	e, err := m.env.EnvFor(scope)
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		out[k] = v
	}
	return out, nil
}

// secretSource resolves the secret source for the active environment: the
// single store for the default env, or the base store overlaid by the active
// env's store for a named environment (env wins). active is the store the
// caller already built for the active env (secrets.<active>.age).
func secretSource(cfg *config.Config, active *secrets.Store) SecretSource {
	if cfg.ActiveEnv == "" || cfg.ActiveEnv == config.DefaultEnv {
		return active
	}
	base := secrets.New(
		config.SecretsPathFor(config.DefaultEnv),
		cfg.Secrets.KeyringService,
		cfg.Secrets.KeyringUser,
		config.KeyFile(),
		cfg.Secrets.AgeRecipient,
	)
	return multiSource{base: base, env: active}
}

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
