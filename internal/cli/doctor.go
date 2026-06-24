package cli

import (
	"fmt"
	"os/exec"
	"sort"

	"github.com/kelsos/rweb/internal/proc"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/kelsos/rweb/internal/worktree"
	"github.com/spf13/cobra"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check tools, repos and the secret store",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			ok := true

			for _, t := range []struct{ name, bin string }{
				{"uv", cfg.Tools.UV}, {"pnpm", cfg.Tools.PNPM},
				{"docker", cfg.Tools.Docker}, {"make", cfg.Tools.Make},
				{"cargo", cfg.Tools.Cargo},
			} {
				if _, e := exec.LookPath(t.bin); e != nil {
					ok = false
					fmt.Printf("✗ tool %-7s (%s) not on PATH\n", t.name, t.bin)
				} else {
					fmt.Printf("✓ tool %-7s\n", t.name)
				}
			}

			// nest and rotki_com_e2e are optional services; only required when set.
			optional := map[string]bool{"nest": true, "rotki_com_e2e": true}
			for _, r := range cfg.RepoRefs() {
				// cfg.Repos paths are already resolved to the selected worktree.
				switch {
				case *r.Path == "":
					if optional[r.Key] {
						fmt.Printf("· repo %-16s not configured (optional)\n", r.Key)
						continue
					}
					ok = false
					fmt.Printf("✗ repo %-16s not configured\n", r.Key)
				case !worktree.IsWorkTree(*r.Path):
					ok = false
					fmt.Printf("✗ repo %-16s not a git work tree: %s\n", r.Key, *r.Path)
				default:
					if sel := cfg.Worktrees[r.Key]; sel != "" {
						fmt.Printf("✓ repo %-16s worktree %q → %s\n", r.Key, sel, *r.Path)
					} else {
						fmt.Printf("✓ repo %-16s\n", r.Key)
					}
				}
			}

			// Flag secret-looking keys sitting in the plaintext [env.*] layer.
			for _, scope := range secrets.Scopes() {
				keys := make([]string, 0, len(cfg.Env[scope]))
				for k := range cfg.Env[scope] {
					if looksLikeSecret(k) {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Printf("⚠ env [%s] %s looks like a secret stored in plaintext (use `rweb secret set %s %s`)\n", scope, k, scope, k)
				}
			}

			for _, l := range proc.DepLocks(cfg) {
				if l.Exists {
					fmt.Printf("✓ lockfile %-16s\n", l.Name)
				} else {
					// Non-fatal: dep-sync simply skips a target with no lockfile.
					fmt.Printf("⚠ lockfile %-16s missing: %s (dep-sync will skip)\n", l.Name, l.Path)
				}
			}

			st := storeFromCfg(cfg)
			if keys, e := st.Keys(); e != nil {
				ok = false
				fmt.Printf("✗ secrets: %v\n", e)
			} else {
				n := 0
				for _, ks := range keys {
					n += len(ks)
				}
				fmt.Printf("✓ secrets store readable (%d keys / %d scopes)\n", n, len(keys))
			}

			if !ok {
				return fmt.Errorf("doctor found problems")
			}
			fmt.Println("\nAll checks passed.")
			return nil
		},
	}
}
