package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/db"
	"github.com/kelsos/rweb/internal/proc"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

func e2eCmd() *cobra.Command {
	var public, ui, noReset, keepUp bool
	c := &cobra.Command{
		Use:   "e2e",
		Short: "Run end-to-end tests (private full-stack suite by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			if public {
				return runPublicE2E(cfg, ui)
			}
			return runPrivateE2E(cfg, storeFromCfg(cfg), ui, !noReset, keepUp)
		},
	}
	c.Flags().BoolVar(&public, "public", false, "run the public self-bootstrapping suite in rotki.com instead")
	c.Flags().BoolVar(&ui, "ui", false, "run the interactive (UI) test runner")
	c.Flags().BoolVar(&noReset, "no-db-reset", false, "skip the fresh-DB reset before the private suite")
	c.Flags().BoolVar(&keepUp, "keep-up", false, "leave the stack running after the suite finishes")
	return c
}

// runPrivateE2E brings up the full stack, waits for traefik, resets the DB, then
// runs the configured private Playwright suite against https://localhost.
func runPrivateE2E(cfg *config.Config, st *secrets.Store, ui, reset, keepUp bool) error {
	if cfg.Repos.RotkiComE2E == "" {
		return fmt.Errorf("repos.rotki_com_e2e not set in config")
	}

	if err := proc.Up(cfg, st, "e2e", proc.UpOpts{Migrate: true}); err != nil {
		return err
	}
	if !keepUp {
		defer func() { _ = proc.Down(cfg, false) }()
	}

	traefik := fmt.Sprintf("localhost:%d", cfg.Ports.TraefikHTTPS)
	fmt.Printf("→ waiting for traefik on %s\n", traefik)
	if err := proc.WaitTCP(traefik, 60*time.Second); err != nil {
		return err
	}

	if reset {
		fmt.Println("→ resetting database")
		if err := db.Reset(cfg, st); err != nil {
			return err
		}
	}

	name, rest := e2eCommand(cfg, ui)
	fmt.Printf("→ running private suite: %s %v\n", name, rest)
	return runIn(cfg.Repos.RotkiComE2E, name, rest...)
}

// runPublicE2E runs the self-bootstrapping suite that lives in rotki.com/main.
func runPublicE2E(cfg *config.Config, ui bool) error {
	if cfg.Repos.RotkiCom == "" {
		return fmt.Errorf("repos.rotki_com not set in config")
	}
	target := "test:e2e"
	if ui {
		target = "test:e2e:ui"
	}
	return runIn(cfg.Repos.RotkiCom, cfg.Tools.PNPM, target)
}

func e2eCommand(cfg *config.Config, ui bool) (string, []string) {
	cmd := cfg.E2E.Command
	if ui && len(cfg.E2E.UICommand) > 0 {
		cmd = cfg.E2E.UICommand
	}
	if len(cmd) == 0 {
		return cfg.Tools.PNPM, []string{"test:e2e"}
	}
	return cmd[0], cmd[1:]
}

func runIn(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = e2eEnv()
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// e2eEnv is the ambient env plus the macOS TMPDIR fix; the Playwright suites
// start their own Nuxt dev server and hit the same socket-path limit. Appending
// last is what makes the override stick: exec resolves duplicate keys to the
// last occurrence.
func e2eEnv() []string {
	env := os.Environ()
	for k, v := range proc.ShortTmpdir() {
		env = append(env, k+"="+v)
	}
	return env
}

func reviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Build the static site and serve it production-like for review",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			if err := proc.Up(cfg, storeFromCfg(cfg), "review-static", proc.UpOpts{Migrate: true}); err != nil {
				return err
			}
			url := fmt.Sprintf("http://localhost:%d", cfg.Review.Port)
			fmt.Printf("\nReview site: %s\n", url)
			return browserOpen(url)
		},
	}
}
