package cli

import (
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

func TestCompleteProfiles(t *testing.T) {
	withTempConfig(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	names, directive := completeProfiles(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, "full") || !contains(names, "web-only") {
		t.Errorf("expected default profile keys, got %v", names)
	}
	// Nothing to complete once the profile is chosen.
	if names, _ := completeProfiles(nil, []string{"full"}, ""); names != nil {
		t.Errorf("no completion past the profile arg, got %v", names)
	}
}

func TestServiceUniverse(t *testing.T) {
	cfg := config.Default()
	cfg.Repos.Nest = "" // nest omitted when its repo is unset
	names := serviceUniverse(cfg)
	for _, want := range []string{"docker", "django", "nuxt", "go-dev"} {
		if !contains(names, want) {
			t.Errorf("expected %q in service universe, got %v", want, names)
		}
	}
	if contains(names, "nest") {
		t.Errorf("nest should be absent when its repo is unset, got %v", names)
	}
	// Once the repo is configured, nest joins the universe.
	cfg.Repos.Nest = "/some/nest"
	if !contains(serviceUniverse(cfg), "nest") {
		t.Error("nest should be present when its repo is configured")
	}
}

func TestCompleteLogServices_FallsBackAndFilters(t *testing.T) {
	withTempConfig(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	// No services are tracked under the temp state dir, so it falls back to the
	// full universe.
	names, directive := completeLogServices(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, "django") {
		t.Errorf("expected fallback to service universe, got %v", names)
	}
	// A name already on the command line is not offered again.
	if names, _ := completeLogServices(nil, []string{"django"}, ""); contains(names, "django") {
		t.Errorf("already-chosen service should be filtered out, got %v", names)
	}
}

func TestCompleteEnvNames(t *testing.T) {
	withTempConfig(t)
	cfg := config.Default()
	cfg.Environments = map[string]config.EnvProfile{"staging": {}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// includeDefault=true offers the base env plus named ones.
	names, directive := completeEnvNames(true)(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, config.DefaultEnv) || !contains(names, "staging") {
		t.Errorf("expected default + staging, got %v", names)
	}

	// includeDefault=false drops the base env (can't be created/deleted).
	names, _ = completeEnvNames(false)(nil, nil, "")
	if contains(names, config.DefaultEnv) {
		t.Errorf("base env must be excluded, got %v", names)
	}
	if !contains(names, "staging") {
		t.Errorf("expected staging, got %v", names)
	}
}

func TestCompleteScopeArg(t *testing.T) {
	// First arg completes to the secret scopes.
	names, directive := completeScopeArg(nil)(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	for _, want := range secrets.Scopes() {
		if !contains(names, want) {
			t.Errorf("expected scope %q, got %v", want, names)
		}
	}

	// Past the scope, it delegates to next.
	called := false
	next := func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		called = true
		return []string{"KEY"}, cobra.ShellCompDirectiveNoFileComp
	}
	names, _ = completeScopeArg(next)(nil, []string{"django"}, "")
	if !called || !contains(names, "KEY") {
		t.Errorf("expected delegation to next completer, got %v (called=%v)", names, called)
	}

	// A nil next yields no further completions.
	if names, _ := completeScopeArg(nil)(nil, []string{"django"}, ""); names != nil {
		t.Errorf("nil next should yield no completions, got %v", names)
	}
}

func TestCompleteScopeThenFile(t *testing.T) {
	names, directive := completeScopeThenFile(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || !contains(names, secrets.ScopeDjango) {
		t.Errorf("arg0 should complete to scopes with NoFileComp, got %v / %v", names, directive)
	}
	// The <file> arg gets shell file completion (Default directive, no values).
	names, directive = completeScopeThenFile(nil, []string{"django"}, "")
	if names != nil || directive != cobra.ShellCompDirectiveDefault {
		t.Errorf("file arg should defer to file completion, got %v / %v", names, directive)
	}
}

func TestCompleteEnvKeys(t *testing.T) {
	withTempConfig(t)
	cfg := config.Default()
	cfg.Env = map[string]map[string]string{"shared": {"REDIS_HOST": "localhost", "FOO": "bar"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	names, directive := completeEnvKeys(nil, []string{"shared"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, "FOO") || !contains(names, "REDIS_HOST") {
		t.Errorf("expected the scope's env keys, got %v", names)
	}
	// A scope with no set keys yields nothing.
	if names, _ := completeEnvKeys(nil, []string{"nuxt"}, ""); names != nil {
		t.Errorf("scope with no keys should complete to nothing, got %v", names)
	}
	// Wrong arg count (scope not yet chosen) yields nothing.
	if names, _ := completeEnvKeys(nil, nil, ""); names != nil {
		t.Errorf("no key completion without the scope arg, got %v", names)
	}
}

func TestCompleteSecretKeys_Happy(t *testing.T) {
	withTempConfig(t)
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	st := storeFromCfg(cfg)
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(secrets.ScopeDjango, "DB_PASS", "s3cret"); err != nil {
		t.Fatal(err)
	}

	names, directive := completeSecretKeys(nil, []string{secrets.ScopeDjango}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, "DB_PASS") {
		t.Errorf("expected the stored secret key, got %v", names)
	}
	// Wrong arg count guards.
	if names, _ := completeSecretKeys(nil, nil, ""); names != nil {
		t.Errorf("no completion without the scope arg, got %v", names)
	}
}

func TestCompleteBackupRefs_LatestAlwaysOffered(t *testing.T) {
	withTempConfig(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	// No backups exist yet, but "latest" is still offered and file completion
	// stays enabled (Default) so an explicit path completes too.
	names, directive := completeBackupRefs(nil, nil, "")
	if directive != cobra.ShellCompDirectiveDefault {
		t.Errorf("directive = %v, want Default (file completion allowed)", directive)
	}
	if !contains(names, "latest") {
		t.Errorf("expected \"latest\" among completions, got %v", names)
	}
	// Nothing to complete past the single ref arg.
	if names, _ := completeBackupRefs(nil, []string{"latest"}, ""); names != nil {
		t.Errorf("no completion past the ref arg, got %v", names)
	}
}
