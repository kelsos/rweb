package cli

import (
	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/kelsos/rweb/internal/tui"
	"github.com/spf13/cobra"
)

func dashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "dashboard",
		Aliases: []string{"dash"},
		Short:   "Open the interactive supervisor dashboard (live status + logs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.RunDashboard(func() (*config.Config, *secrets.Store, error) {
				cfg, err := loadCfg()
				if err != nil {
					return nil, nil, err
				}
				return cfg, storeFromCfg(cfg), nil
			})
		},
	}
}
