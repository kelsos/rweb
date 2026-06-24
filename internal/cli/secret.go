package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/kelsos/rweb/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func secretCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "secret",
		Short: "Manage the age-encrypted secret store",
		Long:  "Manage the age-encrypted secret store.\nRun with no subcommand to open the interactive secret manager (TUI).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretUI()
		},
	}
	c.AddCommand(secretUICmd(), secretSetCmd(), secretRmCmd(), secretListCmd(), secretEditCmd())
	return c
}

func secretUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the interactive secret manager (TUI)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretUI()
		},
	}
}

func runSecretUI() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return tui.RunSecrets(storeFromCfg(cfg))
}

func secretSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <scope> <KEY> [VALUE]",
		Short: "Set a secret (value read hidden from stdin if omitted)",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			scope, key := args[0], args[1]
			val := ""
			if len(args) == 3 {
				val = args[2]
			} else if val, err = readSecretValue(); err != nil {
				return err
			}
			return storeFromCfg(cfg).Set(scope, key, val)
		},
	}
}

func secretRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <scope> <KEY>",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return storeFromCfg(cfg).Rm(args[0], args[1])
		},
	}
}

func secretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret key names (values are never shown)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			keys, err := storeFromCfg(cfg).Keys()
			if err != nil {
				return err
			}
			printed := false
			for _, scope := range secrets.Scopes() {
				if ks := keys[scope]; len(ks) > 0 {
					printed = true
					fmt.Printf("[%s]\n", scope)
					for _, k := range ks {
						fmt.Printf("  %s = ••••\n", k)
					}
				}
			}
			if !printed {
				fmt.Println("(empty)")
			}
			return nil
		},
	}
}

func secretEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Edit the decrypted store in $EDITOR (re-encrypted on save)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			st := storeFromCfg(cfg)
			data, err := st.ReadTOML()
			if err != nil {
				return err
			}

			dir := os.Getenv("XDG_RUNTIME_DIR")
			if dir == "" {
				dir = os.TempDir()
			}
			tmp, err := os.CreateTemp(dir, "rweb-secrets-*.toml")
			if err != nil {
				return err
			}
			path := tmp.Name()
			defer shred(path)
			if err := tmp.Chmod(0o600); err != nil {
				tmp.Close()
				return err
			}
			if _, err := tmp.Write(data); err != nil {
				tmp.Close()
				return err
			}
			tmp.Close()

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			ed := exec.Command(editor, path)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return err
			}

			edited, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return st.WriteTOML(edited)
		},
	}
}

func readSecretValue() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Value (hidden): ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// shred overwrites then removes a temporary plaintext file.
func shred(path string) {
	if fi, err := os.Stat(path); err == nil {
		_ = os.WriteFile(path, make([]byte, fi.Size()), 0o600)
	}
	_ = os.Remove(path)
}
