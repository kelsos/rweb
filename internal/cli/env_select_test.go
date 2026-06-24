package cli

import (
	"testing"

	"github.com/adrg/xdg"
	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/zalando/go-keyring"
)

// withEnvOverride sets the global --env override for one test and restores it.
func withEnvOverride(t *testing.T, v string) {
	t.Helper()
	prev := envOverride
	envOverride = v
	t.Cleanup(func() { envOverride = prev })
}

func staging(desc string) config.EnvProfile {
	return config.EnvProfile{Description: desc, Env: map[string]map[string]string{}}
}

func TestApplyEnv(t *testing.T) {
	base := &config.Config{Environments: map[string]config.EnvProfile{"staging": staging("")}}

	cases := []struct {
		name     string
		sticky   string
		override string
		want     string
	}{
		{"unset falls back to default", "", "", config.DefaultEnv},
		{"sticky named env", "staging", "", "staging"},
		{"stale sticky falls back to default", "ghost", "", config.DefaultEnv},
		{"flag overrides sticky", "staging", "default", config.DefaultEnv},
		{"flag selects named env", "", "staging", "staging"},
		{"stale flag falls back to default", "", "ghost", config.DefaultEnv},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnvOverride(t, tc.override)
			cfg := *base
			cfg.ActiveEnv = tc.sticky
			applyEnv(&cfg)
			if cfg.ActiveEnv != tc.want {
				t.Errorf("ActiveEnv = %q, want %q", cfg.ActiveEnv, tc.want)
			}
		})
	}
}

func TestEditEnv(t *testing.T) {
	base := &config.Config{Environments: map[string]config.EnvProfile{"staging": staging("")}}

	t.Run("unset targets default", func(t *testing.T) {
		withEnvOverride(t, "")
		cfg := *base
		got, err := editEnv(&cfg)
		if err != nil || got != config.DefaultEnv {
			t.Errorf("got (%q,%v), want default,nil", got, err)
		}
	})
	t.Run("sticky named env targets it", func(t *testing.T) {
		withEnvOverride(t, "")
		cfg := *base
		cfg.ActiveEnv = "staging"
		got, err := editEnv(&cfg)
		if err != nil || got != "staging" {
			t.Errorf("got (%q,%v), want staging,nil", got, err)
		}
	})
	t.Run("flag overrides sticky", func(t *testing.T) {
		withEnvOverride(t, "staging")
		cfg := *base
		cfg.ActiveEnv = config.DefaultEnv
		got, err := editEnv(&cfg)
		if err != nil || got != "staging" {
			t.Errorf("got (%q,%v), want staging,nil", got, err)
		}
	})
	// Unlike applyEnv, editEnv errors on a missing env rather than silently
	// falling back, so you never edit the wrong place.
	t.Run("missing env errors", func(t *testing.T) {
		withEnvOverride(t, "ghost")
		cfg := *base
		if got, err := editEnv(&cfg); err == nil {
			t.Errorf("expected error for missing env, got %q", got)
		}
	})
}

func TestEnvNames(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.EnvProfile{
		"zeta":  staging(""),
		"alpha": staging(""),
	}}
	got := envNames(cfg)
	want := []string{config.DefaultEnv, "alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("envNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("envNames = %v, want %v (default first, rest sorted)", got, want)
		}
	}
}

// withTempConfig points the XDG config/data dirs at a temp dir so config.Load/
// Save and the secret stores write under it, and mocks the OS keyring.
func withTempConfig(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

// The named-environment verbs persist through config.toml and mergedKeys unions
// a named env's own secrets with the base secrets it inherits.
func TestNamedEnvLifecycleAndMergedKeys(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Establish the single age identity once (as `rweb init` does) and persist
	// its recipient: every per-env secret file is then encrypted to it, so they
	// all share one keyring identity. Re-running Init would mint a fresh identity
	// and orphan the base file.
	baseStore := storeFromCfg(cfg)
	recipient, _, err := baseStore.Init()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Secrets.AgeRecipient = recipient
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(secrets.ScopeDjango, "SECRET_KEY", "base"); err != nil {
		t.Fatal(err)
	}

	// `env new staging` persists the overlay.
	newCmd := envNewCmd()
	if err := newCmd.RunE(newCmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Environments["staging"]; !ok {
		t.Fatalf("env new did not persist staging: %v", reloaded.Environments)
	}

	// `env use staging` makes it sticky.
	useCmd := envUseCmd()
	if err := useCmd.RunE(useCmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ActiveEnv != "staging" {
		t.Fatalf("env use did not stick: ActiveEnv=%q", reloaded.ActiveEnv)
	}

	// Give staging its own secret in secrets.staging.age. A second Init would mint
	// a fresh keyring identity and orphan the base file, so instead seed an empty
	// store encrypted to the shared recipient (WriteTOML only encrypts); Set then
	// reads it with the existing identity.
	stagingStore := storeFromCfg(reloaded)
	if err := stagingStore.WriteTOML([]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := stagingStore.Set(secrets.ScopeDjango, "DB_PASS", "stagingpw"); err != nil {
		t.Fatal(err)
	}

	// mergedKeys for the active (staging) env unions inherited base keys with the
	// env's own — neither should mask the other.
	keys, err := mergedKeys(reloaded)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range keys[secrets.ScopeDjango] {
		got[k] = true
	}
	if !got["SECRET_KEY"] {
		t.Errorf("mergedKeys should inherit base SECRET_KEY, got %v", keys[secrets.ScopeDjango])
	}
	if !got["DB_PASS"] {
		t.Errorf("mergedKeys should include staging's own DB_PASS, got %v", keys[secrets.ScopeDjango])
	}

	// `rm-env staging --purge-secrets` removes the overlay, reverts the active
	// selection, and deletes the secret file.
	rmCmd := envRmEnvCmd()
	if err := rmCmd.Flags().Set("purge-secrets", "true"); err != nil {
		t.Fatal(err)
	}
	if err := rmCmd.RunE(rmCmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Environments["staging"]; ok {
		t.Errorf("rm-env should delete the overlay")
	}
	if reloaded.ActiveEnv == "staging" {
		t.Errorf("rm-env should revert the active selection away from the deleted env")
	}
}

func TestSeedPostureEnv(t *testing.T) {
	env := map[string]map[string]string{}
	n := seedPostureEnv(env)
	if n != 4 {
		t.Errorf("seedPostureEnv should set 4 values, got %d", n)
	}
	if env[secrets.ScopeGoBackend]["TESTING"] != "true" {
		t.Errorf("go-backend TESTING not seeded: %v", env[secrets.ScopeGoBackend])
	}
	if env[secrets.ScopeGoBackend]["SPONSORSHIP_ENABLED"] != "true" {
		t.Errorf("go-backend SPONSORSHIP_ENABLED not seeded: %v", env[secrets.ScopeGoBackend])
	}
	if env[secrets.ScopeDjango]["USE_CRYPTO_TESTNET"] != "1" {
		t.Errorf("django USE_CRYPTO_TESTNET not seeded: %v", env[secrets.ScopeDjango])
	}
	if env[secrets.ScopeDjango]["BRAINTREE_PRODUCTION"] != "False" {
		t.Errorf("django BRAINTREE_PRODUCTION not seeded: %v", env[secrets.ScopeDjango])
	}
}

// `env new` seeds the dev testnet posture by default; --no-posture creates a
// bare env; --from clones the source instead of seeding.
func TestEnvNewPostureSeeding(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := envNewCmd()
		for _, a := range args[1:] {
			if a == "--no-posture" {
				if err := cmd.Flags().Set("no-posture", "true"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := cmd.RunE(cmd, args[:1]); err != nil {
			t.Fatal(err)
		}
	}

	run("dev")
	run("bare", "--no-posture")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments["dev"].Env[secrets.ScopeGoBackend]["TESTING"] != "true" {
		t.Errorf("env new should seed posture by default: %v", cfg.Environments["dev"].Env)
	}
	if len(cfg.Environments["bare"].Env) != 0 {
		t.Errorf("--no-posture should create a bare overlay: %v", cfg.Environments["bare"].Env)
	}

	// --from clones the (posture-bearing) source rather than re-seeding.
	fromCmd := envNewCmd()
	if err := fromCmd.Flags().Set("from", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := fromCmd.RunE(fromCmd, []string{"clone"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments["clone"].Env[secrets.ScopeGoBackend]["TESTING"] != "true" {
		t.Errorf("--from clone should inherit source posture: %v", cfg.Environments["clone"].Env)
	}
}
