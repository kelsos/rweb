package stackenv

import (
	"path/filepath"
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/zalando/go-keyring"
)

// fakeSource is an in-memory SecretSource for testing the merge logic without
// touching the real (agent-opaque) secret store.
type fakeSource map[string]map[string]string

func (f fakeSource) EnvFor(scope string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f[scope] {
		out[k] = v
	}
	return out, nil
}

func TestDerivedDjangoBootDefaults(t *testing.T) {
	cfg := config.Default()
	got := Derived(cfg, secrets.ScopeDjango)
	// Django settings.py raises ValueError when these are unset; rweb must
	// supply dev-safe defaults so a fresh checkout with no .env files boots.
	want := map[string]string{
		"DB_TYPE":                 "postgres",
		"DJANGO_DEBUG":            "True",
		"DOMAIN":                  "localhost",
		"UPLOADED_BACKUPS_FOLDER": "data/backups",
		"BRAINTREE_PRODUCTION":    "False",
		"EMAIL_TYPE":              "MAILHOG",
		"EMAIL_HOST":              "localhost",
		"EMAIL_PORT":              "1025",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Derived[django][%s] = %q, want %q", k, got[k], v)
		}
	}
	if got["RECAPTCHA_PUBLIC_KEY"] != RecaptchaTestSiteKey {
		t.Errorf("django RECAPTCHA_PUBLIC_KEY should default to the test site key")
	}
	if n := Derived(cfg, secrets.ScopeNuxt); n["NUXT_PUBLIC_RECAPTCHA_SITE_KEY"] != RecaptchaTestSiteKey {
		t.Errorf("nuxt NUXT_PUBLIC_RECAPTCHA_SITE_KEY should default to the test site key")
	}
}

func TestBaselineEnvOverridesDerived(t *testing.T) {
	cfg := config.Default()
	cfg.Env = map[string]map[string]string{
		secrets.ScopeDjango: {"EMAIL_TYPE": "DEBUG", "EXTRA": "1"},
	}
	got := Baseline(cfg, secrets.ScopeDjango)
	if got["EMAIL_TYPE"] != "DEBUG" {
		t.Errorf("[env.django] should override the derived default: EMAIL_TYPE=%q", got["EMAIL_TYPE"])
	}
	if got["EXTRA"] != "1" || got["DB_TYPE"] != "postgres" {
		t.Errorf("baseline should merge derived and [env.django]: %v", got)
	}
}

func TestOverlay(t *testing.T) {
	cfg := config.Default()
	cfg.Environments = map[string]config.EnvProfile{
		"staging": {Env: map[string]map[string]string{
			secrets.ScopeDjango: {"DOMAIN": "staging.local"},
		}},
	}

	// The base/default env has no overlay.
	cfg.ActiveEnv = ""
	if ov := Overlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("default env should have no overlay, got %v", ov)
	}
	cfg.ActiveEnv = config.DefaultEnv
	if ov := Overlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("explicit default env should have no overlay, got %v", ov)
	}

	// An unknown active env yields no overlay (degrades to base).
	cfg.ActiveEnv = "ghost"
	if ov := Overlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("unknown env should have no overlay, got %v", ov)
	}

	// A known named env returns its per-scope map, and nothing for other scopes.
	cfg.ActiveEnv = "staging"
	if ov := Overlay(cfg, secrets.ScopeDjango); ov["DOMAIN"] != "staging.local" {
		t.Errorf("named env overlay = %v, want DOMAIN=staging.local", ov)
	}
	if ov := Overlay(cfg, secrets.ScopeNuxt); len(ov) != 0 {
		t.Errorf("scope with no overlay should be empty, got %v", ov)
	}
}

func TestMultiSourceEnvWins(t *testing.T) {
	base := fakeSource{secrets.ScopeDjango: {"DB_PASS": "base", "SECRET_KEY": "base-only"}}
	env := fakeSource{secrets.ScopeDjango: {"DB_PASS": "env"}}
	m := multiSource{base: base, env: env}

	got, err := m.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if got["DB_PASS"] != "env" {
		t.Errorf("env overlay should win on conflict: DB_PASS=%q", got["DB_PASS"])
	}
	if got["SECRET_KEY"] != "base-only" {
		t.Errorf("base-only secret should be inherited: SECRET_KEY=%q", got["SECRET_KEY"])
	}
}

func TestSourceSelection(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	active := secrets.New(filepath.Join(dir, "secrets.staging.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")

	// The default/base env uses the active store directly (no merge wrapper).
	cfg := config.Default()
	cfg.ActiveEnv = config.DefaultEnv
	if src := Source(cfg, active); src != SecretSource(active) {
		t.Errorf("default env should use the active store directly, got %T", src)
	}

	// A named env wraps base+active in a multiSource (active = the env overlay).
	cfg.ActiveEnv = "staging"
	src := Source(cfg, active)
	ms, ok := src.(multiSource)
	if !ok {
		t.Fatalf("named env should yield a multiSource, got %T", src)
	}
	if ms.env != SecretSource(active) {
		t.Errorf("named env's overlay source should be the active store")
	}
}
