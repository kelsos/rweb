package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/db"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

func dbCmd() *cobra.Command {
	c := &cobra.Command{Use: "db", Short: "Database & migration lifecycle"}
	c.AddCommand(
		dbAction("create", "Create the postgres role + database", db.Create),
		dbAction("migrate", "Run Django migrations", db.Migrate),
		dbAction("reset", "Drop, recreate and migrate (fresh empty DB)", db.Reset),
		dbAction("superuser", "Create a Django superuser", db.Superuser),
		dbAction("shell", "Open the Django dbshell", db.Shell),
		dbBackupCmd(),
		dbRestoreCmd(),
		dbBackupsCmd(),
	)
	return c
}

func dbAction(use, short string, fn func(*config.Config, *secrets.Store) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			return fn(cfg, storeFromCfg(cfg))
		},
	}
}

func dbBackupCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "backup",
		Short: "Dump the database to a compressed backup file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			path, err := db.Backup(cfg, storeFromCfg(cfg), name)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Backed up to %s\n", path)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "backup name (default: <db>-<timestamp>)")
	return c
}

func dbRestoreCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "restore [name|latest|path]",
		Short: "Restore the database from a backup (destructive)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			ref := "latest"
			if len(args) == 1 {
				ref = args[0]
			}
			path, err := db.ResolveBackup(cfg, ref)
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf("Restore %s into database %q? This overwrites current data.",
				filepath.Base(path), cfg.DB.Name)) {
				fmt.Fprintln(os.Stderr, "aborted")
				return nil
			}
			if err := db.Restore(cfg, storeFromCfg(cfg), path); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Restored from %s\n", path)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func dbBackupsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "backups",
		Aliases: []string{"list"},
		Short:   "List available database backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			backups, err := db.ListBackups(cfg)
			if err != nil {
				return err
			}
			if len(backups) == 0 {
				fmt.Fprintf(os.Stdout, "No backups in %s\n", cfg.BackupDir())
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSIZE\tCREATED")
			for _, b := range backups {
				fmt.Fprintf(w, "%s\t%s\t%s\n", b.Name, humanSize(b.Size), b.ModTime.Format("2006-01-02 15:04:05"))
			}
			return w.Flush()
		},
	}
}

// confirm prompts on stderr for a yes/no answer, defaulting to no.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// humanSize renders a byte count in binary (KiB/MiB/...) units.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
