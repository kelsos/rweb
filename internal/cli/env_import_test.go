package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
)

func TestParseDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `
# a comment
export DOMAIN=localhost
DB_PASS="s3cr3t"
QUOTED='single'
EMPTY=
DB_PASS=overridden
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, err := parseDotenv(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var order []string
	for _, p := range pairs {
		got[p.Key] = p.Val
		order = append(order, p.Key)
	}
	if got["DOMAIN"] != "localhost" {
		t.Errorf("export prefix not stripped: %q", got["DOMAIN"])
	}
	if got["DB_PASS"] != "overridden" {
		t.Errorf("last assignment should win: %q", got["DB_PASS"])
	}
	if got["QUOTED"] != "single" {
		t.Errorf("single quotes not stripped: %q", got["QUOTED"])
	}
	if got["EMPTY"] != "" {
		t.Errorf("empty value: %q", got["EMPTY"])
	}
	// A duplicate key keeps its first position (value updated in place).
	if len(order) != 4 || order[1] != "DB_PASS" {
		t.Errorf("unexpected key order %v", order)
	}
}

func TestParseDotenvMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("NOEQUALS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotenv(path); err == nil {
		t.Error("a line without = should error")
	}
}

// import routes secret-looking keys to the encrypted store and the rest to the
// plaintext [env.*] config, and skips keys that already exist unless overwriting.
func TestEnvImportRoutesAndSkips(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	st := storeFromCfg(cfg)
	recipient, _, err := st.Init()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Secrets.AgeRecipient = recipient
	// Pre-existing values that import should skip without --overwrite.
	cfg.Env = map[string]map[string]string{secrets.ScopeDjango: {"DOMAIN": "keepme"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(secrets.ScopeDjango, "SECRET_KEY", "keep-secret"); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	body := "DOMAIN=newdomain\nUPLOADED_BACKUPS_FOLDER=data/backups\nSECRET_KEY=new-secret\nDB_PASS=fresh\n"
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := envImportCmd()
	if err := cmd.RunE(cmd, []string{secrets.ScopeDjango, envFile}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Non-secret, new → imported to [env.*]; non-secret, existing → skipped.
	if reloaded.Env[secrets.ScopeDjango]["UPLOADED_BACKUPS_FOLDER"] != "data/backups" {
		t.Errorf("new non-secret key should be imported: %v", reloaded.Env[secrets.ScopeDjango])
	}
	if reloaded.Env[secrets.ScopeDjango]["DOMAIN"] != "keepme" {
		t.Errorf("existing non-secret key should be skipped, got %q", reloaded.Env[secrets.ScopeDjango]["DOMAIN"])
	}
	// Secret-looking keys must NOT leak into the plaintext config.
	if _, leaked := reloaded.Env[secrets.ScopeDjango]["DB_PASS"]; leaked {
		t.Error("secret-looking key leaked into plaintext [env.*]")
	}

	// New secret-looking key → store; existing secret → skipped (value preserved).
	env, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASS"] != "fresh" {
		t.Errorf("new secret should be stored: DB_PASS=%q", env["DB_PASS"])
	}
	if env["SECRET_KEY"] != "keep-secret" {
		t.Errorf("existing secret should be skipped, got %q", env["SECRET_KEY"])
	}
}

func TestEnvImportOverwrite(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	cfg.Env = map[string]map[string]string{secrets.ScopeDjango: {"DOMAIN": "old"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("DOMAIN=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := envImportCmd()
	if err := cmd.Flags().Set("overwrite", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{secrets.ScopeDjango, envFile}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Env[secrets.ScopeDjango]["DOMAIN"] != "new" {
		t.Errorf("--overwrite should replace: %q", reloaded.Env[secrets.ScopeDjango]["DOMAIN"])
	}
}

// A dry-run writes nothing — neither plaintext config nor the secret store.
func TestEnvImportDryRun(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	st := storeFromCfg(cfg)
	if _, _, err := st.Init(); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("DOMAIN=x\nDB_PASS=y\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := envImportCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{secrets.ScopeDjango, envFile}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Env[secrets.ScopeDjango]) != 0 {
		t.Errorf("dry-run must not write env: %v", reloaded.Env[secrets.ScopeDjango])
	}
	env, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["DB_PASS"]; ok {
		t.Error("dry-run must not write secrets")
	}
}

// Importing secrets into a fresh named env (no secret file yet) must fail
// cleanly rather than orphaning anything.
func TestEnvImportNamedEnvWithoutSecretFile(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "staging")

	cfg := config.Default()
	cfg.Environments = map[string]config.EnvProfile{"staging": {Env: map[string]map[string]string{}}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("DB_PASS=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := envImportCmd()
	if err := cmd.RunE(cmd, []string{secrets.ScopeDjango, envFile}); err == nil {
		t.Error("importing a secret into an env with no secret store should error")
	}
}

// Importing a key that classifies as a secret must remove any stale plaintext
// copy of the same key from [env.*], so a secret value never lingers on disk.
func TestEnvImportPurgesStalePlaintext(t *testing.T) {
	withTempConfig(t)
	withEnvOverride(t, "")

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	st := storeFromCfg(cfg)
	recipient, _, err := st.Init()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Secrets.AgeRecipient = recipient
	// A secret-named key sitting in plaintext [env.*] (e.g. previously --force'd).
	cfg.Env = map[string]map[string]string{secrets.ScopeDjango: {"DB_PASS": "old-plaintext"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("DB_PASS=fromimport\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := envImportCmd()
	if err := cmd.Flags().Set("overwrite", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{secrets.ScopeDjango, envFile}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Env[secrets.ScopeDjango]["DB_PASS"]; ok {
		t.Error("stale plaintext DB_PASS should be removed from [env.*] after secret import")
	}
	env, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASS"] != "fromimport" {
		t.Errorf("secret store should hold the imported value: %q", env["DB_PASS"])
	}
}
