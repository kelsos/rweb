package cli

import (
	"github.com/kelsos/rweb/internal/tui"
	"github.com/spf13/cobra"
)

func dashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "dashboard",
		Aliases: []string{"dash"},
		Short:   "Open the interactive supervisor dashboard (live status + logs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return tui.RunDashboard(cfg, storeFromCfg(cfg))
		},
	}
}
