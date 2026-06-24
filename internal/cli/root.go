// Package cli wires up the rweb command-line interface.
package cli

import (
	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

// Execute runs the root command.
func Execute() error {
	root := &cobra.Command{
		Use:           "rweb",
		Short:         "rotki.com dev-stack orchestrator",
		Long:          "rweb starts, stops and supervises the full rotki.com local development stack,\nwith centrally-managed, agent-opaque secrets.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringArrayVar(&worktreeOverrides, "worktree", nil,
		"target a repo's git worktree for this run, e.g. --worktree rotki_com=develop (repeatable)")
	root.PersistentFlags().StringVar(&envOverride, "env", "",
		"target a named environment for this run (overrides the sticky `rweb env use` selection)")
	root.AddCommand(
		initCmd(),
		doctorCmd(),
		configCmd(),
		secretCmd(),
		envCmd(),
		dbCmd(),
		upCmd(),
		downCmd(),
		syncCmd(),
		statusCmd(),
		restartCmd(),
		logsCmd(),
		dockerCmd(),
		openCmd(),
		execCmd(),
		e2eCmd(),
		reviewCmd(),
		dashboardCmd(),
		worktreeCmd(),
	)
	// Add an `install` subcommand under Cobra's auto-generated `completion`
	// command (which only prints), so users can install/update completions in
	// one step. InitDefaultCompletionCmd materializes that command now; Execute
	// sees it already present and won't re-add it.
	root.InitDefaultCompletionCmd()
	for _, sub := range root.Commands() {
		if sub.Name() == "completion" {
			sub.AddCommand(completionInstallCmd(root))
			break
		}
	}
	return root.Execute()
}

// storeFromCfg builds a secrets.Store for the active environment's secret file
// (secrets.age for the default env, secrets.<name>.age for a named one).
func storeFromCfg(cfg *config.Config) *secrets.Store {
	return secrets.New(
		config.SecretsPathFor(cfg.ActiveEnv),
		cfg.Secrets.KeyringService,
		cfg.Secrets.KeyringUser,
		config.KeyFile(),
		cfg.Secrets.AgeRecipient,
	)
}
