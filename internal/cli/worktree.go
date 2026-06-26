package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/worktree"
	"github.com/spf13/cobra"
)

// worktreeOverrides holds --worktree repo=name flags, applied on top of the
// sticky selection in config for the current invocation only.
var worktreeOverrides []string

// loadCfg loads config.toml and resolves each repo path to its selected git
// worktree (sticky selection overlaid with --worktree flags). Commands that run
// against the repos use this instead of config.Load so they transparently target
// the active worktree; commands that edit config.toml must use config.Load.
func loadCfg() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := applyWorktrees(cfg, worktreeOverrides); err != nil {
		return nil, err
	}
	applyEnv(cfg)
	return cfg, nil
}

// applyWorktrees rewrites cfg.Repos paths in place to the selected worktree
// leaves. A selection that names a worktree the repo does not have falls back to
// the configured path with a warning. cfg.Worktrees itself is left untouched.
func applyWorktrees(cfg *config.Config, overrides []string) error {
	sel := map[string]string{}
	for k, v := range cfg.Worktrees {
		sel[k] = v
	}
	valid := map[string]bool{}
	for _, r := range cfg.RepoRefs() {
		valid[r.Key] = true
	}
	for _, o := range overrides {
		k, v, ok := strings.Cut(o, "=")
		if !ok {
			return fmt.Errorf("invalid --worktree %q (want repo=name)", o)
		}
		k = strings.TrimSpace(k)
		if !valid[k] {
			return fmt.Errorf("unknown repo %q in --worktree (valid: %s)", k, strings.Join(repoKeys(cfg), ", "))
		}
		sel[k] = strings.TrimSpace(v)
	}
	for _, r := range cfg.RepoRefs() {
		want := sel[r.Key]
		if want == "" || *r.Path == "" {
			continue
		}
		dir, matched, err := worktree.Resolve(*r.Path, want)
		if err != nil {
			return fmt.Errorf("repo %s: %w", r.Key, err)
		}
		if !matched {
			fmt.Fprintf(os.Stderr, "⚠ worktree %q not found for %s; using configured %s\n", want, r.Key, *r.Path)
			continue
		}
		*r.Path = dir
	}
	return nil
}

func repoKeys(cfg *config.Config) []string {
	keys := make([]string, 0, len(cfg.RepoRefs()))
	for _, r := range cfg.RepoRefs() {
		keys = append(keys, r.Key)
	}
	return keys
}

func repoRef(cfg *config.Config, key string) (config.RepoRef, bool) {
	for _, r := range cfg.RepoRefs() {
		if r.Key == key {
			return r, true
		}
	}
	return config.RepoRef{}, false
}

func worktreeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "worktree",
		Short: "Manage per-repo git worktree selection",
		Long: "Select which git worktree each repo's stack runs against, without editing\n" +
			"its configured path. Worktrees are discovered from git, so this works for\n" +
			"plain checkouts (one worktree) and multi-worktree layouts alike.",
	}
	c.AddCommand(worktreeListCmd(), worktreeUseCmd(), worktreeClearCmd())
	return c
}

// completeRepoKeys completes the first positional argument with the configured
// repo keys. Used by worktree subcommands that take a repo as their first arg.
func completeRepoKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return repoKeys(cfg), cobra.ShellCompDirectiveNoFileComp
}

// completeWorktreeNames completes a worktree selection for the repo named in
// args[0], offering both branch names and worktree directory base names (the two
// forms worktree.Resolve accepts).
func completeWorktreeNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ref, ok := repoRef(cfg, args[0])
	if !ok || *ref.Path == "" || !worktree.IsWorkTree(*ref.Path) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	trees, err := worktree.List(*ref.Path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var names []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	for _, t := range trees {
		add(t.Branch)
		add(filepath.Base(t.Path))
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func worktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "list [repo]",
		Short:             "List available git worktrees per repo (* marks the active selection)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeRepoKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			filter := ""
			if len(args) == 1 {
				filter = args[0]
				if _, ok := repoRef(cfg, filter); !ok {
					return fmt.Errorf("unknown repo %q (valid: %s)", filter, strings.Join(repoKeys(cfg), ", "))
				}
			}
			for _, r := range cfg.RepoRefs() {
				if filter != "" && r.Key != filter {
					continue
				}
				if *r.Path == "" {
					continue
				}
				fmt.Printf("%s  %s\n", r.Key, *r.Path)
				if !worktree.IsWorkTree(*r.Path) {
					fmt.Printf("  ✗ not a git work tree\n")
					continue
				}
				trees, err := worktree.List(*r.Path)
				if err != nil {
					return err
				}
				selected := cfg.Worktrees[r.Key]
				for _, t := range trees {
					marker := "  "
					if selected != "" && (t.Branch == selected || filepath.Base(t.Path) == selected) {
						marker = "* "
					}
					fmt.Printf("  %s%-24s %s\n", marker, branchLabel(t), t.Path)
				}
				if selected == "" {
					fmt.Printf("  (no selection — using configured path)\n")
				}
			}
			return nil
		},
	}
}

func worktreeUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <repo> <name>",
		Short: "Persist a worktree selection for a repo (sticky across runs)",
		Args:  cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeRepoKeys(cmd, args, toComplete)
			}
			return completeWorktreeNames(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, name := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ref, ok := repoRef(cfg, repo)
			if !ok {
				return fmt.Errorf("unknown repo %q (valid: %s)", repo, strings.Join(repoKeys(cfg), ", "))
			}
			if *ref.Path == "" {
				return fmt.Errorf("repo %s has no configured path", repo)
			}
			dir, matched, err := worktree.Resolve(*ref.Path, name)
			if err != nil {
				return err
			}
			if !matched {
				return fmt.Errorf("worktree %q not found for %s (see `rweb worktree list %s`)", name, repo, repo)
			}
			if cfg.Worktrees == nil {
				cfg.Worktrees = map[string]string{}
			}
			cfg.Worktrees[repo] = name
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ %s → %s  (%s)\n", repo, name, dir)
			return nil
		},
	}
}

func worktreeClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "clear <repo>",
		Short:             "Clear a repo's worktree selection (revert to its configured path)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRepoKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, ok := repoRef(cfg, repo); !ok {
				return fmt.Errorf("unknown repo %q (valid: %s)", repo, strings.Join(repoKeys(cfg), ", "))
			}
			if cfg.Worktrees[repo] == "" {
				fmt.Printf("%s has no worktree selection\n", repo)
				return nil
			}
			delete(cfg.Worktrees, repo)
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ cleared worktree selection for %s\n", repo)
			return nil
		},
	}
}

func branchLabel(t worktree.Tree) string {
	if t.Branch == "" {
		return "(detached)"
	}
	return t.Branch
}
