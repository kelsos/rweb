package cli

import (
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
)

func TestSeedsForMode(t *testing.T) {
	if s, err := seedsForMode("none"); err != nil || s != nil {
		t.Errorf("none should yield no seeds, got %v %v", s, err)
	}
	if s, err := seedsForMode(""); err != nil || s != nil {
		t.Errorf("empty should yield no seeds, got %v %v", s, err)
	}
	ph, err := seedsForMode("placeholders")
	if err != nil || len(ph) != 5 {
		t.Fatalf("placeholders should yield 5 seeds, got %d %v", len(ph), err)
	}
	gen, err := seedsForMode("generate")
	if err != nil || len(gen) != 5 {
		t.Fatalf("generate should yield 5 seeds, got %d %v", len(gen), err)
	}
	if _, err := seedsForMode("bogus"); err == nil {
		t.Error("unknown mode should error")
	}
}

// POSTGRES_PASSWORD seeds the docker postgres and DB_PASS is what Django
// connects with — both modes must keep them equal or the DB won't connect.
func TestSeedsKeepDBPasswordsMatched(t *testing.T) {
	for _, mode := range []string{"placeholders", "generate"} {
		seeds, err := seedsForMode(mode)
		if err != nil {
			t.Fatal(err)
		}
		var pg, db string
		for _, s := range seeds {
			if s.scope == secrets.ScopeShared && s.key == "POSTGRES_PASSWORD" {
				pg = s.val
			}
			if s.scope == secrets.ScopeDjango && s.key == "DB_PASS" {
				db = s.val
			}
		}
		if pg == "" || db == "" || pg != db {
			t.Errorf("%s: POSTGRES_PASSWORD (%q) must equal DB_PASS (%q)", mode, pg, db)
		}
	}
}

func TestGeneratedSeedsAreRandom(t *testing.T) {
	a, err := generatedSeeds()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generatedSeeds()
	if err != nil {
		t.Fatal(err)
	}
	// DJANGO_SECRET_KEY should differ between two generations.
	var ka, kb string
	for _, s := range a {
		if s.key == "DJANGO_SECRET_KEY" {
			ka = s.val
		}
	}
	for _, s := range b {
		if s.key == "DJANGO_SECRET_KEY" {
			kb = s.val
		}
	}
	if ka == "" || ka == kb {
		t.Errorf("generated secret key should be random and non-empty: %q vs %q", ka, kb)
	}
}

// env new --seed-secrets placeholders must create the env's own secret file
// (sharing the base identity, NOT orphaning the base) and fill the required
// secrets so env doctor passes for the new env.
func TestEnvNewSeedsSecrets(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	base := storeFromCfg(cfg)
	recipient, _, err := base.Init()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Secrets.AgeRecipient = recipient
	if err := base.Set(secrets.ScopeShared, "POSTGRES_PASSWORD", "base-pg"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	cmd := envNewCmd()
	if err := cmd.Flags().Set("seed-secrets", "placeholders"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}

	// The staging store has its own required secrets.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	reloaded.ActiveEnv = "staging"
	stg := storeFromCfg(reloaded)
	env, err := stg.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASS"] != "123" || env["DJANGO_SECRET_KEY"] != "dev-insecure-secret-key" {
		t.Errorf("staging django secrets not seeded: %v", env)
	}
	shared, err := stg.EnvFor(secrets.ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	if shared["POSTGRES_PASSWORD"] != "123" {
		t.Errorf("staging POSTGRES_PASSWORD not seeded: %q", shared["POSTGRES_PASSWORD"])
	}

	// The base store must remain decryptable — seeding a new env must NOT have
	// re-Init'd and orphaned it.
	baseEnv, err := base.EnvFor(secrets.ScopeShared)
	if err != nil {
		t.Fatalf("base store orphaned by env seeding: %v", err)
	}
	if baseEnv["POSTGRES_PASSWORD"] != "base-pg" {
		t.Errorf("base secret changed unexpectedly: %q", baseEnv["POSTGRES_PASSWORD"])
	}
}

// Without an identity (no init), env new still creates the environment; it just
// can't create a secret store — and must not crash.
func TestEnvNewNoIdentitySkipsSecretStore(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	cmd := envNewCmd()
	if err := cmd.RunE(cmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["staging"]; !ok {
		t.Error("env should still be created without an identity")
	}
	if fileExists(config.SecretsPathFor("staging")) {
		t.Error("no secret store should be created without an identity")
	}
}

// init --seed-secrets fills the base env's required secrets (so env doctor
// passes immediately) and seeds the dev posture into [env.*].
func TestInitSeedsBaseEnv(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cmd := initCmd()
	if err := cmd.Flags().Set("seed-secrets", "placeholders"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Base required secrets seeded into the default store.
	st := storeFromCfg(cfg)
	shared, err := st.EnvFor(secrets.ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	if shared["POSTGRES_PASSWORD"] != "123" || shared["REDIS_PASSWORD"] != "1234" {
		t.Errorf("base shared secrets not seeded: %v", shared)
	}
	dj, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if dj["DB_PASS"] != "123" || dj["DJANGO_SECRET_KEY"] == "" {
		t.Errorf("base django secrets not seeded: %v", dj)
	}
	// Posture seeded into base [env.*].
	if cfg.Env[secrets.ScopeGoBackend]["TESTING"] != "true" {
		t.Errorf("init should seed posture into base env: %v", cfg.Env)
	}
}

func TestInitNoPostureNoSeed(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cmd := initCmd()
	if err := cmd.Flags().Set("no-posture", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Env) != 0 {
		t.Errorf("--no-posture should leave base [env.*] empty: %v", cfg.Env)
	}
	// Default --seed-secrets=none → no secrets seeded.
	st := storeFromCfg(cfg)
	shared, err := st.EnvFor(secrets.ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 0 {
		t.Errorf("no seeding requested, store should be empty: %v", shared)
	}
}
