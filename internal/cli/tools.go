package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/kelsos/rweb/internal/db"
	"github.com/spf13/cobra"
)

func openCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "open <site|mailpit|traefik|report>",
		Short:     "Open a service URL in the browser",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"site", "mailpit", "traefik", "report"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			targets := map[string]string{
				"site":    "https://localhost",
				"mailpit": "http://localhost:8025",
				"traefik": "http://localhost:8080",
				"report":  "file://" + filepath.Join(cfg.Repos.RotkiCom, "packages", "website", "playwright-report", "index.html"),
			}
			u, ok := targets[args[0]]
			if !ok {
				return fmt.Errorf("unknown target %q (site|mailpit|traefik|report)", args[0])
			}
			fmt.Println("opening", u)
			return browserOpen(u)
		},
	}
}

func browserOpen(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	return c.Start()
}

func execCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <target> -- <args...>",
		Short: "Run a command in a service context with env injected (targets: django)",
		Args:  cobra.MinimumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return []string{"django"}, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			switch args[0] {
			case "django":
				return db.Manage(cfg, storeFromCfg(cfg), args[1:]...)
			default:
				return fmt.Errorf("unknown exec target %q (supported: django)", args[0])
			}
		},
	}
}
