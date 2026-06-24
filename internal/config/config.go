// Package config loads and persists rweb's cross-platform configuration,
// stored under the XDG config directory as config.toml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/adrg/xdg"
)

const appName = "rweb"

// Config is the on-disk configuration for rweb.
type Config struct {
	Repos    Repos              `toml:"repos"`
	Tools    Tools              `toml:"tools"`
	Ports    Ports              `toml:"ports"`
	Secrets  Secrets            `toml:"secrets"`
	DB       DB                 `toml:"db"`
	Review   Review             `toml:"review"`
	E2E      E2E                `toml:"e2e"`
	Profiles map[string]Profile `toml:"profiles"`
	// Worktrees is a sticky per-repo git worktree selection, keyed by the repo
	// key used in RepoRefs (e.g. "rotki_com" -> "develop"). It overlays nothing
	// in config.toml when empty; an unset/unmatched selection falls back to the
	// configured repo path. Managed by `rweb worktree use/clear`.
	Worktrees map[string]string `toml:"worktrees,omitempty"`
	// Env holds non-secret environment values to inject per secret scope, keyed
	// scope -> KEY -> value (e.g. env["django"]["DB_HOST"] = "localhost"). It is
	// the managed baseline a service starts from, so a fresh checkout with no
	// repo .env files still has its config; repo .env files (when present)
	// override it, and secrets always win. This is the base (default env) layer.
	// Managed by `rweb env set/rm`.
	Env map[string]map[string]string `toml:"env,omitempty"`
	// ActiveEnv selects which named environment overlays the base. Empty or
	// "default" means the base [env.*] + secrets.age. Sticky via `rweb env use`;
	// the global --env flag overrides it per-run (resolved into this field
	// in-memory by loadCfg, never persisted from there).
	ActiveEnv string `toml:"active_env,omitempty"`
	// Environments holds named non-secret overlays keyed by env name; each env's
	// secrets live in a sibling secrets.<name>.age. Managed by `rweb env
	// new/rm-env` and written by `rweb env set --env <name>`.
	Environments map[string]EnvProfile `toml:"environments,omitempty"`
}

// DefaultEnv is the name of the base environment (flat [env.*] + secrets.age).
const DefaultEnv = "default"

// EnvProfile is a named environment's non-secret overlay; its secrets live in a
// sibling secrets.<name>.age file. Env is keyed scope -> KEY -> value and sits
// above repo .env files at spawn (an explicit env selection wins).
type EnvProfile struct {
	Description string                       `toml:"description,omitempty"`
	Env         map[string]map[string]string `toml:"env,omitempty"`
}

// RepoRef pairs a repo's stable config key with a pointer to its configured
// path, so callers can iterate the orchestrated repos uniformly and resolve
// worktree selections in place.
type RepoRef struct {
	Key  string
	Path *string
}

// RepoRefs returns the orchestrated repos in a stable order. The pointer lets a
// caller rewrite a repo's path to a resolved worktree leaf without the rest of
// the codebase knowing worktrees exist.
func (c *Config) RepoRefs() []RepoRef {
	return []RepoRef{
		{"rotkehlchen_web", &c.Repos.RotkehlchenWeb},
		{"rotki_com", &c.Repos.RotkiCom},
		{"nest", &c.Repos.Nest},
		{"rotki_com_e2e", &c.Repos.RotkiComE2E},
	}
}

// Review configures the review-static profile (production-like SSG serve).
type Review struct {
	StaticDir string `toml:"static_dir"` // relative to rotki_com
	Port      int    `toml:"port"`       // port the Go backend serves on
}

// E2E configures how the private full-stack Playwright suite is run.
type E2E struct {
	Command   []string `toml:"command"`    // default suite command
	UICommand []string `toml:"ui_command"` // interactive variant
}

// Repos holds the filesystem paths to the repositories rweb orchestrates.
type Repos struct {
	RotkehlchenWeb string `toml:"rotkehlchen_web"`
	RotkiCom       string `toml:"rotki_com"`
	Nest           string `toml:"nest"`          // optional service
	RotkiComE2E    string `toml:"rotki_com_e2e"` // private full-stack Playwright repo
}

// Tools holds the names/paths of external executables rweb shells out to.
type Tools struct {
	UV     string `toml:"uv"`
	PNPM   string `toml:"pnpm"`
	Docker string `toml:"docker"`
	Make   string `toml:"make"`
	Cargo  string `toml:"cargo"` // for the Rust nest service
}

// Ports is used for pre-flight conflict detection and health probes.
type Ports struct {
	Postgres     int `toml:"postgres"`
	Redis        int `toml:"redis"`
	Django       int `toml:"django"`
	GoDev        int `toml:"go_dev"`
	Nuxt         int `toml:"nuxt"`
	Nest         int `toml:"nest"`
	TraefikHTTPS int `toml:"traefik_https"`
}

// Secrets describes where the age identity lives.
type Secrets struct {
	AgeRecipient   string `toml:"age_recipient"`   // public key, used to encrypt
	KeyringService string `toml:"keyring_service"` // OS keychain service name
	KeyringUser    string `toml:"keyring_user"`    // OS keychain account name
}

// DB holds local development database connection defaults (password is a secret).
type DB struct {
	Name      string `toml:"name"`
	User      string `toml:"user"`
	Host      string `toml:"host"`
	Port      int    `toml:"port"`
	BackupDir string `toml:"backup_dir"` // override for dump storage (default: XDG data dir)
}

// BackupDir resolves where database dumps are stored, honouring an explicit
// db.backup_dir override and otherwise defaulting under the XDG data directory.
func (c *Config) BackupDir() string {
	if c.DB.BackupDir != "" {
		return c.DB.BackupDir
	}
	return filepath.Join(xdg.DataHome, appName, "backups")
}

// Profile is an ordered set of services to run.
type Profile struct {
	Services []string `toml:"services"`
	WebAPI   string   `toml:"webapi,omitempty"` // "local" | "remote"
}

// Default returns a config pre-populated with sensible defaults.
func Default() *Config {
	return &Config{
		Tools: Tools{UV: "uv", PNPM: "pnpm", Docker: "docker", Make: "make", Cargo: "cargo"},
		Ports: Ports{
			Postgres: 5432, Redis: 6379, Django: 8000,
			GoDev: 3000, Nuxt: 3001, Nest: 30221, TraefikHTTPS: 443,
		},
		Secrets: Secrets{KeyringService: appName, KeyringUser: "age-identity"},
		DB:      DB{Name: "rotkehlchen_db", User: "rotkehlchen", Host: "localhost", Port: 5432},
		Review:  Review{StaticDir: "packages/website/.output/public", Port: 3000},
		E2E:     E2E{Command: []string{"pnpm", "test:e2e"}, UICommand: []string{"pnpm", "test:e2e:ui"}},
		Profiles: map[string]Profile{
			"full":          {Services: []string{"docker", "django", "huey", "go-dev", "nuxt"}},
			"web-only":      {Services: []string{"go-dev", "nuxt"}},
			"backend-only":  {Services: []string{"docker", "django", "huey"}},
			"review-static": {Services: []string{"docker", "django", "huey", "build-web", "go-serve"}, WebAPI: "local"},
			"e2e":           {Services: []string{"docker", "django", "huey", "go-dev", "nuxt"}},
		},
	}
}

// Dir is the rweb config directory.
func Dir() string { return filepath.Join(xdg.ConfigHome, appName) }

// Path is the config.toml path.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// SecretsPath is the age-encrypted secrets file path for the base/default env.
func SecretsPath() string { return filepath.Join(Dir(), "secrets.age") }

// SecretsPathFor resolves the age-encrypted secrets file for an environment.
// The default/base env uses secrets.age; a named env uses secrets.<name>.age.
func SecretsPathFor(name string) string {
	if name == "" || name == DefaultEnv {
		return SecretsPath()
	}
	return filepath.Join(Dir(), "secrets."+name+".age")
}

// KeyFile is the fallback age identity path used when no OS keychain is available.
func KeyFile() string { return filepath.Join(Dir(), "age.key") }

// Exists reports whether a config file is already present.
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// Load reads config.toml, applying defaults for any fields the file omits.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil, fmt.Errorf("read config (run `rweb init`?): %w", err)
	}
	cfg := Default()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	return cfg, nil
}

// Save writes config.toml, creating the config directory if needed.
func Save(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(Path(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
