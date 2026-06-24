package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/spf13/cobra"
)

// dotenvPair is one KEY=VALUE parsed from a .env file, kept in file order so the
// import preview is deterministic.
type dotenvPair struct {
	Key string
	Val string
}

// parseDotenv reads a .env-style file: KEY=VALUE per line, ignoring blanks and
// # comments, tolerating a leading `export `, and stripping matching single or
// double quotes around the value. Later duplicates win (last assignment wins),
// matching shell sourcing.
func parseDotenv(path string) ([]dotenvPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := map[string]int{}
	var pairs []dotenvPair
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			return nil, fmt.Errorf("%s:%d: not KEY=VALUE: %q", path, ln, line)
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if idx, ok := seen[key]; ok {
			pairs[idx].Val = val
			continue
		}
		seen[key] = len(pairs)
		pairs = append(pairs, dotenvPair{Key: key, Val: val})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return pairs, nil
}

// importAction is what the import would do with one key.
type importAction struct {
	pair   dotenvPair
	secret bool   // routed to the encrypted store (vs plaintext [env.*])
	verb   string // "set" (new), "overwrite", or "skip" (exists, no --overwrite)
}

func envImportCmd() *cobra.Command {
	var overwrite, dryRun bool
	c := &cobra.Command{
		Use:   "import <scope> <file>",
		Short: "Import a .env file into a scope, routing secret-looking keys to the encrypted store",
		Long: "Import KEY=VALUE pairs from a .env file into a scope. Keys whose name looks\n" +
			"sensitive (PASS/SECRET/TOKEN/…) go to the encrypted secret store; the rest go\n" +
			"to the plaintext [env.*] config. Existing values are skipped unless --overwrite.\n" +
			"Use --env to import into a named environment.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, file := args[0], args[1]
			if !validScope(scope) {
				return fmt.Errorf("unknown scope %q (valid: %v)", scope, secrets.Scopes())
			}
			pairs, err := parseDotenv(file)
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Resolve which environment we're importing into and point the secret
			// store at its file (storeFromCfg keys off cfg.ActiveEnv).
			name, target, err := envTarget(cfg)
			if err != nil {
				return err
			}
			cfg.ActiveEnv = name

			existingEnv := map[string]bool{}
			for k := range target[scope] {
				existingEnv[k] = true
			}
			existingSecret := map[string]bool{}
			haveSecretFile := fileExists(config.SecretsPathFor(name))
			st := storeFromCfg(cfg)
			if haveSecretFile {
				keys, err := st.Keys()
				if err != nil {
					return err
				}
				for _, k := range keys[scope] {
					existingSecret[k] = true
				}
			}

			plan := make([]importAction, 0, len(pairs))
			needSecretWrite := false
			for _, p := range pairs {
				a := importAction{pair: p, secret: looksLikeSecret(p.Key)}
				exists := existingEnv[p.Key]
				if a.secret {
					exists = existingSecret[p.Key]
				}
				switch {
				case exists && !overwrite:
					a.verb = "skip"
				case exists:
					a.verb = "overwrite"
				default:
					a.verb = "set"
				}
				if a.secret && a.verb != "skip" {
					needSecretWrite = true
				}
				plan = append(plan, a)
			}

			// Secret writes require an existing store file (Set reads it first); a
			// fresh named env has none yet. Fail before mutating anything.
			if needSecretWrite && !haveSecretFile {
				return fmt.Errorf("%senv has no secret store yet (%s); create it before importing secrets",
					envLabel(name), config.SecretsPathFor(name))
			}

			label := envLabel(name)
			if dryRun {
				fmt.Printf("dry-run: would import %d key(s) into %s[%s]:\n", len(plan), label, scope)
				printImportPlan(plan)
				return nil
			}

			var setN, secretN, skipN int
			dirty := false
			for _, a := range plan {
				if a.verb == "skip" {
					skipN++
					continue
				}
				if a.secret {
					if err := st.Set(scope, a.pair.Key, a.pair.Val); err != nil {
						return fmt.Errorf("set secret %s: %w", a.pair.Key, err)
					}
					secretN++
					// Don't leave a stale plaintext copy of a value now stored
					// encrypted (it would linger in config.toml on disk).
					if _, ok := target[scope][a.pair.Key]; ok {
						delete(target[scope], a.pair.Key)
						dirty = true
					}
					continue
				}
				if target[scope] == nil {
					target[scope] = map[string]string{}
				}
				target[scope][a.pair.Key] = a.pair.Val
				setN++
				dirty = true
			}
			if dirty {
				if err := config.Save(cfg); err != nil {
					return err
				}
			}
			fmt.Printf("✓ imported into %s[%s]: %d env, %d secret, %d skipped\n", label, scope, setN, secretN, skipN)
			if skipN > 0 && !overwrite {
				fmt.Println("  (skipped keys already exist; rerun with --overwrite to replace)")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&overwrite, "overwrite", false, "replace values that already exist")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be imported without writing")
	return c
}

func printImportPlan(plan []importAction) {
	for _, a := range plan {
		dest := "env"
		if a.secret {
			dest = "secret"
		}
		marker := "+"
		switch a.verb {
		case "skip":
			marker = "·"
		case "overwrite":
			marker = "~"
		}
		// Secret values are never echoed.
		shown := a.pair.Val
		if a.secret {
			shown = "********"
		}
		fmt.Printf("  %s %-6s %-32s = %s  [%s]\n", marker, dest, a.pair.Key, shown, a.verb)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
