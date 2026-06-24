package secrets

import (
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	st := New(filepath.Join(dir, "secrets.age"), "rweb-test", "id", filepath.Join(dir, "age.key"), "")
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSetReadKeys(t *testing.T) {
	st := newTestStore(t)
	if err := st.Set(ScopeDjango, "DB_PASS", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ScopeDjango, "DB_USER", "rotki"); err != nil {
		t.Fatal(err)
	}

	m, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if m[ScopeDjango]["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q", m[ScopeDjango]["DB_PASS"])
	}

	// Keys returns sorted names, never values.
	keys, err := st.Keys()
	if err != nil {
		t.Fatal(err)
	}
	got := keys[ScopeDjango]
	if len(got) != 2 || got[0] != "DB_PASS" || got[1] != "DB_USER" {
		t.Fatalf("keys = %v, want sorted [DB_PASS DB_USER]", got)
	}
}

func TestEnvForMergesSharedThenScope(t *testing.T) {
	st := newTestStore(t)
	// shared provides a base; the scope overrides a shared key and adds its own.
	mustSet(t, st, ScopeShared, "REDIS_HOST", "shared-redis")
	mustSet(t, st, ScopeShared, "POSTGRES_PASSWORD", "pw")
	mustSet(t, st, ScopeDjango, "REDIS_HOST", "django-redis")
	mustSet(t, st, ScopeDjango, "DJANGO_SECRET_KEY", "k")

	env, err := st.EnvFor(ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if env["POSTGRES_PASSWORD"] != "pw" {
		t.Errorf("shared key not inherited: %q", env["POSTGRES_PASSWORD"])
	}
	if env["REDIS_HOST"] != "django-redis" {
		t.Errorf("scope should override shared: REDIS_HOST=%q", env["REDIS_HOST"])
	}
	if env["DJANGO_SECRET_KEY"] != "k" {
		t.Errorf("scope-only key missing: %q", env["DJANGO_SECRET_KEY"])
	}
}

func TestRmDeletesKey(t *testing.T) {
	st := newTestStore(t)
	mustSet(t, st, ScopeNuxt, "NUXT_PUBLIC_GOOGLE_CLIENT_ID", "x")
	if err := st.Rm(ScopeNuxt, "NUXT_PUBLIC_GOOGLE_CLIENT_ID"); err != nil {
		t.Fatal(err)
	}
	keys, err := st.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys[ScopeNuxt]) != 0 {
		t.Errorf("key should be gone, have %v", keys[ScopeNuxt])
	}
}

func TestTOMLRoundTrip(t *testing.T) {
	st := newTestStore(t)
	if err := st.WriteTOML([]byte("[shared]\nREDIS_HOST = \"r\"\n")); err != nil {
		t.Fatal(err)
	}
	b, err := st.ReadTOML()
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if m[ScopeShared]["REDIS_HOST"] != "r" {
		t.Errorf("round-trip lost value; toml=%q", string(b))
	}

	// Invalid TOML must be rejected without clobbering the store.
	if err := st.WriteTOML([]byte("not = = valid")); err == nil {
		t.Error("expected error for invalid TOML")
	}
	if m2, _ := st.Read(); m2[ScopeShared]["REDIS_HOST"] != "r" {
		t.Error("store should be unchanged after a rejected write")
	}
}

func mustSet(t *testing.T, st *Store, scope, k, v string) {
	t.Helper()
	if err := st.Set(scope, k, v); err != nil {
		t.Fatal(err)
	}
}
