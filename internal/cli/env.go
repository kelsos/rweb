package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

// secretNameTokens are substrings (case-insensitive) in a key name that suggest
// the value is sensitive and belongs in the encrypted secret store, not the
// plaintext [env.*] config. Public keys like NUXT_PUBLIC_*_SITE_KEY or
// *_CLIENT_ID deliberately don't match (no PRIVATE/SECRET/PASS/TOKEN token).
var secretNameTokens = []string{"PASSWORD", "PASSWD", "PASSPHRASE", "PASS", "SECRET", "PRIVATE_KEY", "PRIVATEKEY", "TOKEN", "CREDENTIAL"}

// looksLikeSecret reports whether a key name looks sensitive by naming pattern.
func looksLikeSecret(key string) bool {
	u := strings.ToUpper(key)
	// NUXT_PUBLIC_* values are baked into the client bundle — public by
	// definition — so they are never secrets, even if the name contains a token
	// like TOKEN/KEY (e.g. a hypothetical NUXT_PUBLIC_*_TOKEN).
	if strings.HasPrefix(u, "NUXT_PUBLIC_") {
		return false
	}
	for _, tok := range secretNameTokens {
		if strings.Contains(u, tok) {
			return true
		}
	}
	return false
}

func envCmd() *cobra.Command {
	c := &cobra.Command{Use: "env", Short: "Environment / secret diagnostics"}
	c.AddCommand(envSetCmd(), envRmCmd(), envListCmd(), envImportCmd())
	c.AddCommand(envUseCmd(), envCurrentCmd(), envEnvsCmd(), envNewCmd(), envRmEnvCmd())
	c.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Diff required keys against the store (names only, never values)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			applyEnv(cfg)
			have, err := mergedKeys(cfg)
			if err != nil {
				return err
			}
			missing := 0
			for _, scope := range secrets.Scopes() {
				present := map[string]bool{}
				for _, k := range have[scope] {
					present[k] = true
				}
				for _, req := range secrets.Manifest[scope] {
					if present[req] {
						continue
					}
					if optionalSecrets[req] {
						fmt.Printf("· [%s] %s not set (optional)\n", scope, req)
						continue
					}
					fmt.Printf("✗ [%s] missing %s\n", scope, req)
					missing++
				}
			}
			if missing == 0 {
				fmt.Println("✓ all required keys present")
				return nil
			}
			return fmt.Errorf("%d required key(s) missing", missing)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "manifest",
		Short: "List the keys rweb expects (required/optional secrets + optional non-secret env)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("required secrets (env doctor fails when missing):")
			for _, scope := range secrets.Scopes() {
				req := requiredManifest(scope)
				if len(req) == 0 {
					continue
				}
				fmt.Printf("[%s]\n", scope)
				for _, k := range req {
					fmt.Printf("  %s\n", k)
				}
			}
			fmt.Println("\noptional secrets (feature-gated / dev-defaulted):")
			for _, scope := range secrets.Scopes() {
				opt := optionalManifest(scope)
				if len(opt) == 0 {
					continue
				}
				fmt.Printf("[%s]\n", scope)
				for _, k := range opt {
					fmt.Printf("  %s\n", k)
				}
			}
			fmt.Println("\noptional env (non-secret flags/tunables; set with `rweb env set`):")
			for _, scope := range secrets.Scopes() {
				entries := optionalEnv[scope]
				if len(entries) == 0 {
					continue
				}
				fmt.Printf("[%s]\n", scope)
				for _, e := range entries {
					fmt.Printf("  %-40s %-12s %s\n", e.Key, e.Def, e.Desc)
				}
			}
			return nil
		},
	})
	return c
}

// validScope reports whether scope is one of the known secret scopes; the
// non-secret env layer is keyed by the same scopes so injection lines up.
func validScope(scope string) bool {
	for _, s := range secrets.Scopes() {
		if s == scope {
			return true
		}
	}
	return false
}

// envLabel renders an environment prefix for messages: "" for the base env,
// "(name) " for a named env.
func envLabel(name string) string {
	if name == config.DefaultEnv {
		return ""
	}
	return "(" + name + ") "
}

// envTarget resolves the non-secret env map that env set/rm edit for the active
// (or --env) environment: cfg.Env for the base, or the named env's overlay. The
// returned map is wired into cfg so mutating it and saving cfg persists.
func envTarget(cfg *config.Config) (string, map[string]map[string]string, error) {
	name, err := editEnv(cfg)
	if err != nil {
		return "", nil, err
	}
	if name == config.DefaultEnv {
		if cfg.Env == nil {
			cfg.Env = map[string]map[string]string{}
		}
		return name, cfg.Env, nil
	}
	p := cfg.Environments[name]
	if p.Env == nil {
		p.Env = map[string]map[string]string{}
	}
	cfg.Environments[name] = p
	return name, p.Env, nil
}

func envSetCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "set <scope> <KEY> <VALUE>",
		Short: "Set a non-secret env value injected into a scope's services",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, key, val := args[0], args[1], args[2]
			if !validScope(scope) {
				return fmt.Errorf("unknown scope %q (valid: %v)", scope, secrets.Scopes())
			}
			if looksLikeSecret(key) && !force {
				fmt.Fprintf(os.Stderr,
					"⚠ %q looks like a secret. `rweb env` stores values in PLAINTEXT in %s.\n  For sensitive values use the encrypted store instead:  rweb secret set %s %s\n",
					key, config.Path(), scope, key)
				if !confirm("Store it in plaintext anyway?") {
					return fmt.Errorf("aborted; use `rweb secret set %s %s` (encrypted), or `rweb env set --force` to store in plaintext", scope, key)
				}
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name, target, err := envTarget(cfg)
			if err != nil {
				return err
			}
			if target[scope] == nil {
				target[scope] = map[string]string{}
			}
			target[scope][key] = val
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ %s[%s] %s = %s\n", envLabel(name), scope, key, val)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "store even if the key name looks like a secret (plaintext)")
	return c
}

func envRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <scope> <KEY>",
		Short: "Remove a non-secret env value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, key := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name, target, err := envTarget(cfg)
			if err != nil {
				return err
			}
			if _, ok := target[scope][key]; !ok {
				fmt.Printf("%s[%s] %s is not set\n", envLabel(name), scope, key)
				return nil
			}
			delete(target[scope], key)
			if len(target[scope]) == 0 {
				delete(target, scope)
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ removed %s[%s] %s\n", envLabel(name), scope, key)
			return nil
		},
	}
}

func envListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the non-secret env values injected per scope (values shown)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name, err := editEnv(cfg)
			if err != nil {
				return err
			}
			layer := cfg.Env
			if name != config.DefaultEnv {
				layer = cfg.Environments[name].Env
			}
			if name != config.DefaultEnv {
				fmt.Printf("environment: %s (overlay on default)\n", name)
			}
			printed := false
			for _, scope := range secrets.Scopes() {
				kv := layer[scope]
				if len(kv) == 0 {
					continue
				}
				printed = true
				fmt.Printf("[%s]\n", scope)
				keys := make([]string, 0, len(kv))
				for k := range kv {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Printf("  %s = %s\n", k, kv[k])
				}
			}
			if !printed {
				fmt.Println("(no non-secret env configured — set with `rweb env set <scope> <KEY> <VALUE>`)")
			}
			return nil
		},
	}
}
