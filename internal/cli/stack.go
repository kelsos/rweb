package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/kelsos/rweb/internal/proc"
	"github.com/spf13/cobra"
)

func upCmd() *cobra.Command {
	var noMigrate, noNest, noSync bool
	c := &cobra.Command{
		Use:   "up [profile]",
		Short: "Start a profile (default: full), supervised and health-ordered",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			profile := "full"
			if len(args) == 1 {
				profile = args[0]
			}
			// nest is opt-out: it auto-joins when its repo is configured (and the
			// profile runs the DB it needs); --no-nest skips it for this run.
			return proc.Up(cfg, storeFromCfg(cfg), profile, proc.UpOpts{
				Migrate:  !noMigrate,
				WithNest: cfg.Repos.Nest != "" && !noNest,
				SyncDeps: !noSync,
			})
		},
	}
	c.Flags().BoolVar(&noMigrate, "no-migrate", false, "skip the automatic DB migrate step")
	c.Flags().BoolVar(&noNest, "no-nest", false, "skip the nest service even when it's configured")
	c.Flags().BoolVar(&noSync, "no-sync", false, "skip the automatic dependency sync")
	return c
}

func syncCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "sync",
		Short: "Install dependencies (frozen) for repos whose lockfile changed",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return proc.DepSync(cfg, force)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "sync every repo regardless of lockfile changes")
	return c
}

func downCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "down",
		Short: "Stop rweb-managed services (add --all to also stop the docker infra)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return proc.Down(cfg, all)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "also stop the docker infra (postgres/redis/mailpit/traefik)")
	return c
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show status of tracked services",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return proc.Status(cfg)
		},
	}
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart a single service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return proc.Restart(cfg, storeFromCfg(cfg), args[0])
		},
	}
}

func logsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs [service...]",
		Short: "Tail captured logs (all tracked services, multiplexed, when none given)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return proc.Tail(args[0], follow)
			}
			names := args
			if len(names) == 0 {
				cfg, err := loadCfg()
				if err != nil {
					return err
				}
				if names = proc.TrackedNames(cfg); len(names) == 0 {
					fmt.Println("no services running")
					return nil
				}
			}
			return multiLogs(names, follow)
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return c
}

// logColors is the palette cycled through to tag each service's lines.
var logColors = []lipgloss.Color{"10", "12", "13", "14", "11", "9", "6", "5"}

// multiLogs renders an interleaved, color-tagged view of several services' logs.
// When following, Ctrl-C stops the stream cleanly.
func multiLogs(names []string, follow bool) error {
	width := 0
	style := map[string]lipgloss.Style{}
	for i, n := range names {
		if len(n) > width {
			width = len(n)
		}
		style[n] = lipgloss.NewStyle().Bold(true).Foreground(logColors[i%len(logColors)])
	}

	ch, stop := proc.MultiTail(names, follow)
	if follow {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sig
			stop()
		}()
		defer signal.Stop(sig)
	}
	for line := range ch {
		tag := style[line.Service].Render(fmt.Sprintf("%-*s", width, line.Service))
		fmt.Printf("%s │ %s\n", tag, line.Text)
	}
	return nil
}

func dockerCmd() *cobra.Command {
	c := &cobra.Command{Use: "docker", Short: "Manage the docker infra only"}
	c.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Start docker infra (postgres, redis, mailpit, traefik)",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadCfg()
				if err != nil {
					return err
				}
				return proc.StartDocker(cfg, storeFromCfg(cfg))
			},
		},
		&cobra.Command{
			Use:   "down",
			Short: "Stop docker infra",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadCfg()
				if err != nil {
					return err
				}
				return proc.StopDocker(cfg)
			},
		},
	)
	return c
}
