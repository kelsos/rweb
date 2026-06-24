package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kelsos/rweb/internal/config"
	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Inspect and edit rweb configuration"}
	c.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(config.Path())
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the effective configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				return toml.NewEncoder(os.Stdout).Encode(cfg)
			},
		},
		configSetRepoCmd(),
		configEditCmd(),
	)
	return c
}

// configSetRepoCmd sets one [repos] path by key, expanding ~ and relative paths
// to an absolute path. An empty path clears the entry (skips that optional
// service).
func configSetRepoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-repo <key> <path>",
		Short: "Set a repository path (key: rotkehlchen_web|rotki_com|nest|rotki_com_e2e)",
		Long: "Set one [repos] path. The path is expanded (~, relative → absolute).\n" +
			"Pass an empty path (\"\") to clear an optional repo. Point worktree-backed\n" +
			"repos at a leaf, not the container holding .git; run `rweb doctor` after.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, raw := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			var target *string
			var keys []string
			for _, r := range cfg.RepoRefs() {
				keys = append(keys, r.Key)
				if r.Key == key {
					target = r.Path
				}
			}
			if target == nil {
				return fmt.Errorf("unknown repo key %q (valid: %s)", key, strings.Join(keys, ", "))
			}
			path, err := expandPath(raw)
			if err != nil {
				return err
			}
			*target = path
			if err := config.Save(cfg); err != nil {
				return err
			}
			if path == "" {
				fmt.Printf("✓ cleared repos.%s\n", key)
				return nil
			}
			fmt.Printf("✓ repos.%s = %s\n", key, path)
			if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
				fmt.Printf("  ⚠ %s is not an existing directory yet\n", path)
			} else {
				fmt.Println("  run `rweb doctor` to verify it's a git work tree")
			}
			return nil
		},
	}
}

// configEditCmd opens the config file in $EDITOR.
func configEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !config.Exists() {
				return fmt.Errorf("no config at %s yet — run `rweb init` first", config.Path())
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			ed := exec.Command(editor, config.Path())
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			return ed.Run()
		},
	}
}

// expandPath turns ~ and relative paths into an absolute path; "" stays "" so an
// optional repo can be cleared.
func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Abs(p)
}
