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

// Restart and StartDocker resolve a single service by name rather than through
// a profile. They must get the same env layers Up does: a restarted django used
// to lose the derived defaults, [env.*] and the named-environment overlay.
func TestResolveServiceMatchesProfileEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Repos.RotkehlchenWeb = dir
	cfg.Env = map[string]map[string]string{"django": {"DOMAIN": "fromenv"}}
	cfg.Environments = map[string]config.EnvProfile{
		"staging": {Env: map[string]map[string]string{"django": {"DB_PORT": "fromoverlay"}}},
	}
	cfg.ActiveEnv = "staging"
	if err := os.WriteFile(filepath.Join(dir, "localtest_env"), []byte("DB_PORT=fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := resolveService(cfg, "django")
	if err != nil {
		t.Fatal(err)
	}
	env, err := BuildEnv(s, fakeSource{})
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vals[k] = v
		}
	}
	for k, want := range map[string]string{
		"EMAIL_TYPE":       "MAILHOG",     // derived default
		"DOMAIN":           "fromenv",     // [env.django]
		"DB_PORT":          "fromoverlay", // named env beats repo file
		"PYTHONUNBUFFERED": "1",           // service's own env kept
	} {
		if vals[k] != want {
			t.Errorf("%s = %q, want %q", k, vals[k], want)
		}
	}

	docker, err := resolveService(cfg, "docker")
	if err != nil {
		t.Fatal(err)
	}
	if docker.BaseEnv["REDIS_HOST"] != "localhost" {
		t.Errorf("docker should carry the shared baseline, got BaseEnv=%v", docker.BaseEnv)
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
