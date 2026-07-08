package cli

import (
	"sort"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/db"
	"github.com/kelsos/rweb/internal/proc"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

// The completers below back the dynamic shell completion of positional args.
// They all follow the same contract: load whatever they need quietly (a config
// error yields no suggestions rather than noise on the completion line) and
// return ShellCompDirectiveNoFileComp so the shell doesn't fall back to file
// names where a name/key/scope is expected. They use config.Load, not loadCfg,
// so completing never triggers worktree resolution or its stderr warnings.

// completeProfiles completes the single optional arg of `up` with the profile
// keys defined in config.
func completeProfiles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// serviceUniverse returns every service name rweb can build: the union of all
// profiles' service lists, plus nest when its repo is configured (nest is
// injected at runtime rather than listed in a profile).
func serviceUniverse(cfg *config.Config) []string {
	seen := map[string]bool{}
	var names []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	for _, p := range cfg.Profiles {
		for _, s := range p.Services {
			add(s)
		}
	}
	if cfg.Repos.Nest != "" {
		add("nest")
	}
	sort.Strings(names)
	return names
}

// completeServiceNames completes the single service arg of `restart` with every
// buildable service name.
func completeServiceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return serviceUniverse(cfg), cobra.ShellCompDirectiveNoFileComp
}

// completeLogServices completes the repeatable service args of `logs`. It
// prefers the services actually running (what you'd usually tail) and falls back
// to the full universe, in both cases dropping names already on the command line.
func completeLogServices(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := proc.TrackedNames(cfg)
	if len(names) == 0 {
		names = serviceUniverse(cfg)
	}
	chosen := map[string]bool{}
	for _, a := range args {
		chosen[a] = true
	}
	var out []string
	for _, n := range names {
		if !chosen[n] {
			out = append(out, n)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeEnvNames returns a completer for an environment-name arg. When
// includeDefault is false the base environment is omitted (it can't be created
// or deleted).
func completeEnvNames(includeDefault bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, n := range envNames(cfg) {
			if !includeDefault && n == config.DefaultEnv {
				continue
			}
			names = append(names, n)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeScopeArg completes a <scope> positional (arg index 0) with the secret
// scopes, then hands off to next for any later argument. next may be nil when
// there is nothing further to complete.
func completeScopeArg(next func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return secrets.Scopes(), cobra.ShellCompDirectiveNoFileComp
		}
		if next != nil {
			return next(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeSecretKeys completes a <KEY> arg (arg index 1, after a scope) with the
// keys already present in the encrypted store for that scope, so overwriting or
// removing a secret is tab-completable. Values are never read, only names.
func completeSecretKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys, err := storeFromCfg(cfg).Keys()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return keys[args[0]], cobra.ShellCompDirectiveNoFileComp
}

// completeEnvKeys completes a <KEY> arg (arg index 1, after a scope) with the
// plaintext env keys already set for that scope in the active environment.
func completeEnvKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, target, err := envTarget(cfg)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var keys []string
	for k := range target[args[0]] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, cobra.ShellCompDirectiveNoFileComp
}

// completeScopeThenFile completes a <scope> for arg 0, then lets the shell do
// file completion for the following <file> arg (used by `env import`).
func completeScopeThenFile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return secrets.Scopes(), cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		return nil, cobra.ShellCompDirectiveDefault // file completion for <file>
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeBackupRefs completes `db restore` with the known backup names plus the
// literal "latest"; file completion stays enabled so an explicit path still
// completes.
func completeBackupRefs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := loadCfg()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	backups, err := db.ListBackups(cfg)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := []string{"latest"}
	for _, b := range backups {
		names = append(names, b.Name)
	}
	return names, cobra.ShellCompDirectiveDefault
}
