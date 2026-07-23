package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/zalando/go-keyring"
)

func TestServicesFullProfileOrder(t *testing.T) {
	cfg := config.Default()
	got, err := Services(cfg, "full")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	want := []string{"docker", "django", "huey", "go-dev", "nuxt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", names, want)
	}
	if !got[0].Oneshot {
		t.Error("docker should be oneshot")
	}
}

func TestDockerServiceHasPostgresReadinessProbe(t *testing.T) {
	cfg := config.Default()
	s, err := buildService(cfg, "docker")
	if err != nil {
		t.Fatal(err)
	}
	// Postgres must use a real readiness probe (pg_isready), not a bare TCP
	// dial: the mapped port can accept a connection mid-initdb, before the
	// server is ready, which would let migrations run too early.
	if s.Health.Postgres == nil {
		t.Fatal("docker service should have a Postgres readiness probe")
	}
	if s.Health.Postgres.Docker != cfg.Tools.Docker {
		t.Errorf("probe docker = %q, want %q", s.Health.Postgres.Docker, cfg.Tools.Docker)
	}
	if s.Health.Postgres.Dir != cfg.Repos.RotkehlchenWeb {
		t.Errorf("probe dir = %q, want %q", s.Health.Postgres.Dir, cfg.Repos.RotkehlchenWeb)
	}
	// The postgres TCP dial is replaced by the pg_isready probe; only redis
	// should remain as a TCP check.
	for _, addr := range s.Health.TCP {
		if strings.HasSuffix(addr, fmt.Sprintf(":%d", cfg.Ports.Postgres)) {
			t.Errorf("postgres should not be a bare TCP probe, got %q", addr)
		}
	}
}

func TestDerivedEnvDjangoBootDefaults(t *testing.T) {
	cfg := config.Default()
	got := derivedEnv(cfg, secrets.ScopeDjango)
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
			t.Errorf("derivedEnv[django][%s] = %q, want %q", k, got[k], v)
		}
	}
	if got["RECAPTCHA_PUBLIC_KEY"] != recaptchaTestSiteKey {
		t.Errorf("django RECAPTCHA_PUBLIC_KEY should default to the test site key")
	}
	if n := derivedEnv(cfg, secrets.ScopeNuxt); n["NUXT_PUBLIC_RECAPTCHA_SITE_KEY"] != recaptchaTestSiteKey {
		t.Errorf("nuxt NUXT_PUBLIC_RECAPTCHA_SITE_KEY should default to the test site key")
	}
}

func TestReviewStaticProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Repos.RotkiCom = "/repos/rotki.com"
	got, err := Services(cfg, "review-static")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	want := []string{"docker", "django", "huey", "build-web", "go-serve"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", names, want)
	}

	var buildWeb, goServe Service
	for _, s := range got {
		switch s.Name {
		case "build-web":
			buildWeb = s
		case "go-serve":
			goServe = s
		}
	}
	if !buildWeb.Oneshot {
		t.Error("build-web should be oneshot")
	}
	// build-web bakes NUXT_PUBLIC_* at generate time, so it must carry the nuxt
	// scope (and thus the central [env.nuxt] baseline) like the dev server.
	if buildWeb.Scope != secrets.ScopeNuxt {
		t.Errorf("build-web should use the nuxt scope, got %q", buildWeb.Scope)
	}
	if goServe.Oneshot {
		t.Error("go-serve should be long-running")
	}
	if goServe.Env["STATIC_DIR"] != "/repos/rotki.com/packages/website/.output/public" {
		t.Errorf("unexpected STATIC_DIR: %q", goServe.Env["STATIC_DIR"])
	}
	if goServe.Env["PORT"] != "3000" {
		t.Errorf("unexpected PORT: %q", goServe.Env["PORT"])
	}
}

func TestServicesErrors(t *testing.T) {
	cfg := config.Default()
	if _, err := Services(cfg, "nope"); err == nil {
		t.Error("expected error for unknown profile")
	}
	if _, err := buildService(cfg, "nest"); err == nil {
		t.Error("expected error for nest without configured repo")
	}
	if _, err := buildService(cfg, "go-build-serve"); err == nil {
		t.Error("expected not-implemented error for go-build-serve")
	}
}

func TestBuildEnvOverlaysSecrets(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Repos.RotkehlchenWeb = dir
	// repo env file sets DB_HOST; the secret store should override it.
	if err := os.WriteFile(filepath.Join(dir, "localtest_env"), []byte("DB_HOST=fromfile\nDB_PORT=5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := secrets.New(filepath.Join(dir, "secrets.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(secrets.ScopeDjango, "DB_HOST", "fromsecret"); err != nil {
		t.Fatal(err)
	}

	s, err := buildService(cfg, "django")
	if err != nil {
		t.Fatal(err)
	}
	env, err := BuildEnv(s, st)
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			vals[kv[:i]] = kv[i+1:]
		}
	}
	if vals["DB_HOST"] != "fromsecret" {
		t.Errorf("secret should override file: got DB_HOST=%q", vals["DB_HOST"])
	}
	if vals["DB_PORT"] != "5432" {
		t.Errorf("file value should remain: got DB_PORT=%q", vals["DB_PORT"])
	}
}

// A fresh checkout has no repo .env file; the non-secret central env baseline
// must still reach the service, while a repo file (when present) overrides it
// and secrets override everything.
func TestBuildEnvCentralBaseline(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Repos.RotkehlchenWeb = dir
	cfg.Env = map[string]map[string]string{
		"django": {
			"DOMAIN":  "localhost", // only in central env (no repo file at all)
			"DB_HOST": "frombase",  // central baseline; should be overridden below
		},
	}
	// No localtest_env / .env written: simulate a fresh checkout.

	st := secrets.New(filepath.Join(dir, "secrets.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(secrets.ScopeDjango, "DB_HOST", "fromsecret"); err != nil {
		t.Fatal(err)
	}

	svcs, err := Services(cfg, "full")
	if err != nil {
		t.Fatal(err)
	}
	var django Service
	for _, s := range svcs {
		if s.Name == "django" {
			django = s
		}
	}
	if django.BaseEnv["DOMAIN"] != "localhost" {
		t.Fatalf("central env not wired onto service: BaseEnv=%v", django.BaseEnv)
	}

	env, err := BuildEnv(django, st)
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			vals[kv[:i]] = kv[i+1:]
		}
	}
	if vals["DOMAIN"] != "localhost" {
		t.Errorf("central-only value should be injected with no repo file: got DOMAIN=%q", vals["DOMAIN"])
	}
	if vals["DB_HOST"] != "fromsecret" {
		t.Errorf("secret should override central baseline: got DB_HOST=%q", vals["DB_HOST"])
	}
}

// Connection details rweb can compute from config (DB_* from [db], REDIS_HOST)
// are injected automatically — no repo file, no [env.*], no secret needed.
func TestBuildEnvDerivedFromConfig(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Repos.RotkehlchenWeb = dir
	cfg.DB.Host = "db.local"
	cfg.DB.Port = 6543
	cfg.DB.Name = "mydb"
	cfg.DB.User = "myuser"
	// No env files, no cfg.Env, no secrets beyond store init.

	st := secrets.New(filepath.Join(dir, "secrets.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}

	svcs, err := Services(cfg, "full")
	if err != nil {
		t.Fatal(err)
	}
	var django Service
	for _, s := range svcs {
		if s.Name == "django" {
			django = s
		}
	}
	env, err := BuildEnv(django, st)
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			vals[kv[:i]] = kv[i+1:]
		}
	}
	for k, want := range map[string]string{"DB_HOST": "db.local", "DB_PORT": "6543", "DB_NAME": "mydb", "DB_USER": "myuser"} {
		if vals[k] != want {
			t.Errorf("derived %s = %q, want %q", k, vals[k], want)
		}
	}
}

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

func TestEnvOverlay(t *testing.T) {
	cfg := config.Default()
	cfg.Environments = map[string]config.EnvProfile{
		"staging": {Env: map[string]map[string]string{
			secrets.ScopeDjango: {"DOMAIN": "staging.local"},
		}},
	}

	// The base/default env has no overlay.
	cfg.ActiveEnv = ""
	if ov := envOverlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("default env should have no overlay, got %v", ov)
	}
	cfg.ActiveEnv = config.DefaultEnv
	if ov := envOverlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("explicit default env should have no overlay, got %v", ov)
	}

	// An unknown active env yields no overlay (degrades to base).
	cfg.ActiveEnv = "ghost"
	if ov := envOverlay(cfg, secrets.ScopeDjango); ov != nil {
		t.Errorf("unknown env should have no overlay, got %v", ov)
	}

	// A known named env returns its per-scope map, and nothing for other scopes.
	cfg.ActiveEnv = "staging"
	if ov := envOverlay(cfg, secrets.ScopeDjango); ov["DOMAIN"] != "staging.local" {
		t.Errorf("named env overlay = %v, want DOMAIN=staging.local", ov)
	}
	if ov := envOverlay(cfg, secrets.ScopeNuxt); len(ov) != 0 {
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

func TestSecretSourceSelection(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	active := secrets.New(filepath.Join(dir, "secrets.staging.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")

	// The default/base env uses the active store directly (no merge wrapper).
	cfg := config.Default()
	cfg.ActiveEnv = config.DefaultEnv
	if src := secretSource(cfg, active); src != SecretSource(active) {
		t.Errorf("default env should use the active store directly, got %T", src)
	}

	// A named env wraps base+active in a multiSource (active = the env overlay).
	cfg.ActiveEnv = "staging"
	src := secretSource(cfg, active)
	ms, ok := src.(multiSource)
	if !ok {
		t.Fatalf("named env should yield a multiSource, got %T", src)
	}
	if ms.env != SecretSource(active) {
		t.Errorf("named env's overlay source should be the active store")
	}
}

func TestDjangoServicesUnbuffered(t *testing.T) {
	cfg := config.Default()
	for _, name := range []string{"django", "huey"} {
		s, err := buildService(cfg, name)
		if err != nil {
			t.Fatal(err)
		}
		if s.Env["PYTHONUNBUFFERED"] != "1" {
			t.Errorf("%s should set PYTHONUNBUFFERED=1 so logs flush promptly, got %v", name, s.Env)
		}
	}
}

// macOS caps unix socket paths at 104 bytes and the default per-user TMPDIR is
// long enough that Nuxt's vite-node socket overruns it. Other platforms keep
// whatever TMPDIR the user set.
func TestShortTmpdirOnlyOnDarwin(t *testing.T) {
	if got := shortTmpdirFor("darwin")["TMPDIR"]; got != "/tmp" {
		t.Errorf("darwin should pin TMPDIR=/tmp, got %q", got)
	}
	for _, goos := range []string{"linux", "windows"} {
		if got := shortTmpdirFor(goos); got != nil {
			t.Errorf("%s should leave TMPDIR alone, got %v", goos, got)
		}
	}
}

func TestNuxtServiceUsesShortTmpdir(t *testing.T) {
	s, err := buildService(config.Default(), "nuxt")
	if err != nil {
		t.Fatal(err)
	}
	want, ok := ShortTmpdir()["TMPDIR"]
	got, gotOK := s.Env["TMPDIR"]
	if ok != gotOK || got != want {
		t.Errorf("nuxt TMPDIR = %q (set=%v), want %q (set=%v)", got, gotOK, want, ok)
	}
}

// The overlay must land after os.Environ so it beats an inherited long TMPDIR:
// exec resolves duplicate keys to the last occurrence.
func TestBuildEnvTmpdirOverridesInherited(t *testing.T) {
	t.Setenv("TMPDIR", "/var/folders/zn/n9fpvbnj3fn971b5s466fzd40000gn/T/")
	s, err := buildService(config.Default(), "nuxt")
	if err != nil {
		t.Fatal(err)
	}
	s.Env = map[string]string{"TMPDIR": "/tmp"} // force the darwin overlay on any host
	env, err := BuildEnv(s, fakeSource{})
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			last = kv
		}
	}
	if last != "TMPDIR=/tmp" {
		t.Errorf("composed env should resolve TMPDIR to /tmp, got %q", last)
	}
}
