package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/kelsos/rweb/internal/config"
	"github.com/spf13/cobra"
)

// envOverride holds the global --env flag: the named environment to target for
// this run, overriding the sticky active_env selection.
var envOverride string

// applyEnv resolves the active environment (the --env flag over the sticky
// active_env) into cfg.ActiveEnv in-memory. A name with no [environments.<name>]
// falls back to the default env with a warning (so a stale selection degrades
// gracefully rather than erroring). Called by loadCfg for run commands.
func applyEnv(cfg *config.Config) {
	name := cfg.ActiveEnv
	if envOverride != "" {
		name = envOverride
	}
	if name == "" {
		name = config.DefaultEnv
	}
	if name != config.DefaultEnv {
		if _, ok := cfg.Environments[name]; !ok {
			fmt.Fprintf(os.Stderr, "⚠ environment %q not found; using %q\n", name, config.DefaultEnv)
			name = config.DefaultEnv
		}
	}
	cfg.ActiveEnv = name
}

// editEnv returns the environment that env-editing commands target: the --env
// flag over the sticky active_env, defaulting to the base env. Unlike applyEnv
// it errors on a missing named env rather than silently falling back, so you
// never edit the wrong place.
func editEnv(cfg *config.Config) (string, error) {
	name := cfg.ActiveEnv
	if envOverride != "" {
		name = envOverride
	}
	if name == "" || name == config.DefaultEnv {
		return config.DefaultEnv, nil
	}
	if _, ok := cfg.Environments[name]; !ok {
		return "", fmt.Errorf("environment %q does not exist (create it with `rweb env new %s`)", name, name)
	}
	return name, nil
}

// envNames returns the environment names (default + configured), sorted, with
// "default" first.
func envNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Environments)+1)
	for n := range cfg.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	return append([]string{config.DefaultEnv}, names...)
}

// mergedKeys returns the secret key names present for the active environment:
// the active env's own store, unioned with the base store's keys (which a named
// env inherits). Used by `env doctor` so an inherited base secret isn't flagged
// missing. Names only — never values.
func mergedKeys(cfg *config.Config) (map[string][]string, error) {
	active, err := storeFromCfg(cfg).Keys()
	if err != nil {
		return nil, err
	}
	if cfg.ActiveEnv == "" || cfg.ActiveEnv == config.DefaultEnv {
		return active, nil
	}
	baseCfg := *cfg
	baseCfg.ActiveEnv = config.DefaultEnv
	base, err := storeFromCfg(&baseCfg).Keys()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	add := func(m map[string][]string) {
		for scope, keys := range m {
			if seen[scope] == nil {
				seen[scope] = map[string]bool{}
			}
			for _, k := range keys {
				if !seen[scope][k] {
					seen[scope][k] = true
					out[scope] = append(out[scope], k)
				}
			}
		}
	}
	add(base)
	add(active)
	return out, nil
}

func envUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "use <name>",
		Short:             "Select the active environment (sticky across runs)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeEnvNames(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if name != config.DefaultEnv {
				if _, ok := cfg.Environments[name]; !ok {
					return fmt.Errorf("environment %q does not exist (create it with `rweb env new %s`)", name, name)
				}
			}
			cfg.ActiveEnv = name
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ active environment → %s\n", name)
			return nil
		},
	}
}

func envCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the active environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			sticky := cfg.ActiveEnv
			if sticky == "" {
				sticky = config.DefaultEnv
			}
			fmt.Printf("%s\n", sticky)
			if envOverride != "" && envOverride != sticky {
				fmt.Fprintf(os.Stderr, "(overridden this run by --env %s)\n", envOverride)
			}
			return nil
		},
	}
}

func envEnvsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "envs",
		Short: "List environments (* marks the active one)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			active := cfg.ActiveEnv
			if active == "" {
				active = config.DefaultEnv
			}
			for _, n := range envNames(cfg) {
				marker := "  "
				if n == active {
					marker = "* "
				}
				desc := ""
				if p, ok := cfg.Environments[n]; ok && p.Description != "" {
					desc = "  " + p.Description
				}
				fmt.Printf("%s%s%s\n", marker, n, desc)
			}
			return nil
		},
	}
}

func envNewCmd() *cobra.Command {
	var from, desc, seedMode string
	var noPosture bool
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a named environment (overlays the base; secrets in secrets.<name>.age)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == config.DefaultEnv {
				return fmt.Errorf("%q is the base environment and always exists", config.DefaultEnv)
			}
			seeds, err := seedsForMode(seedMode)
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Environments[name]; ok {
				return fmt.Errorf("environment %q already exists", name)
			}
			p := config.EnvProfile{Description: desc, Env: map[string]map[string]string{}}
			// Seed the dev testnet posture into a fresh env; a --from clone instead
			// inherits whatever posture its source already carries.
			seeded := 0
			if from == "" && !noPosture {
				seeded = seedPostureEnv(p.Env)
			}
			if from != "" {
				src := map[string]map[string]string{}
				if from == config.DefaultEnv {
					src = cfg.Env
				} else if fp, ok := cfg.Environments[from]; ok {
					src = fp.Env
				} else {
					return fmt.Errorf("source environment %q does not exist", from)
				}
				for scope, kv := range src {
					p.Env[scope] = map[string]string{}
					for k, v := range kv {
						p.Env[scope][k] = v
					}
				}
			}
			if cfg.Environments == nil {
				cfg.Environments = map[string]config.EnvProfile{}
			}
			cfg.Environments[name] = p
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ created environment %q\n", name)
			if seeded > 0 {
				fmt.Printf("  seeded the dev testnet posture (%d values); disable with --no-posture\n", seeded)
			}

			// Create the env's own secret file up front (encrypted to the existing
			// identity) so `secret set --env %s` works immediately; then seed the
			// required secrets if asked. If there's no identity yet, skip with a hint
			// rather than failing the whole command.
			st, created, serr := ensureEnvSecretStore(cfg, name)
			if serr != nil {
				fmt.Printf("  (no secret store yet — run `rweb init` first, then `rweb --env %s secret set …`)\n", name)
			} else {
				if created {
					fmt.Printf("  created its secret store: %s\n", config.SecretsPathFor(name))
				}
				if len(seeds) > 0 {
					n, err := seedSecrets(st, seeds)
					if err != nil {
						return fmt.Errorf("seed secrets: %w", err)
					}
					fmt.Printf("  seeded %d required secret(s) [%s]\n", n, seedMode)
				}
			}

			fmt.Printf("  set its non-secret values:  rweb env set --env %s <scope> <KEY> <VALUE>\n", name)
			fmt.Printf("  set its secrets:            rweb --env %s secret set <scope> <KEY>\n", name)
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "copy non-secret values from an existing environment")
	c.Flags().StringVar(&desc, "description", "", "a short description for the environment")
	c.Flags().BoolVar(&noPosture, "no-posture", false, "don't seed the dev testnet posture (TESTING/SPONSORSHIP_ENABLED/USE_CRYPTO_TESTNET/…)")
	c.Flags().StringVar(&seedMode, "seed-secrets", "none", "seed required secrets: none|placeholders|generate")
	return c
}

func envRmEnvCmd() *cobra.Command {
	var purgeSecrets bool
	c := &cobra.Command{
		Use:               "rm-env <name>",
		Short:             "Delete a named environment (its overlay; optionally its secret file)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeEnvNames(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == config.DefaultEnv {
				return fmt.Errorf("cannot delete the base %q environment", config.DefaultEnv)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Environments[name]; !ok {
				return fmt.Errorf("environment %q does not exist", name)
			}
			delete(cfg.Environments, name)
			if cfg.ActiveEnv == name {
				cfg.ActiveEnv = ""
				fmt.Printf("  (was active; reverted to %q)\n", config.DefaultEnv)
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ deleted environment %q\n", name)
			if purgeSecrets {
				path := config.SecretsPathFor(name)
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove secret file: %w", err)
				}
				fmt.Printf("✓ removed %s\n", path)
			} else {
				fmt.Printf("  its secret file is kept (%s); rerun with --purge-secrets to delete it\n", config.SecretsPathFor(name))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&purgeSecrets, "purge-secrets", false, "also delete the environment's secrets.<name>.age file")
	return c
}
