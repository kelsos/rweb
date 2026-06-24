package cli

import (
	"fmt"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	var force, noPosture bool
	var seedMode string
	c := &cobra.Command{
		Use:   "init",
		Short: "Scaffold config + age identity (stored in the OS keychain)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.Exists() && !force {
				return fmt.Errorf("config already exists at %s (use --force to overwrite)", config.Path())
			}
			seeds, err := seedsForMode(seedMode)
			if err != nil {
				return err
			}
			cfg := config.Default()
			// Seed the dev testnet posture into the base [env.*] (non-secret), so a
			// fresh init yields a testnet-posture dev stack out of the box.
			if !noPosture {
				if cfg.Env == nil {
					cfg.Env = map[string]map[string]string{}
				}
				seedPostureEnv(cfg.Env)
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			st := secrets.New(config.SecretsPath(), cfg.Secrets.KeyringService, cfg.Secrets.KeyringUser, config.KeyFile(), "")
			recipient, mode, err := st.Init()
			if err != nil {
				return err
			}
			cfg.Secrets.AgeRecipient = recipient
			if err := config.Save(cfg); err != nil {
				return err
			}
			seededN := 0
			if len(seeds) > 0 {
				if seededN, err = seedSecrets(st, seeds); err != nil {
					return fmt.Errorf("seed secrets: %w", err)
				}
			}

			fmt.Printf("Initialized rweb config: %s\n", config.Path())
			switch mode {
			case "keychain":
				fmt.Printf("Age identity stored in OS keychain (%s/%s)\n", cfg.Secrets.KeyringService, cfg.Secrets.KeyringUser)
			case "file":
				fmt.Printf("No OS keychain available — age identity written to %s (0600). This is the weaker mode.\n", config.KeyFile())
			}
			fmt.Printf("Recipient (public key): %s\n", recipient)
			if seededN > 0 {
				fmt.Printf("Seeded %d required secret(s) [%s]\n", seededN, seedMode)
			}
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  1. Set repo paths:   rweb config set-repo rotkehlchen_web <path> (and rotki_com)")
			if seededN > 0 {
				fmt.Println("  2. Verify:           rweb env doctor && rweb doctor")
			} else {
				fmt.Println("  2. Add secrets:      rweb secret set shared POSTGRES_PASSWORD  (or rerun init --seed-secrets)")
				fmt.Println("  3. Verify:           rweb env doctor && rweb doctor")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	c.Flags().BoolVar(&noPosture, "no-posture", false, "don't seed the dev testnet posture into [env.*]")
	c.Flags().StringVar(&seedMode, "seed-secrets", "none", "seed the base env's required secrets: none|placeholders|generate")
	return c
}
