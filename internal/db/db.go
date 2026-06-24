// Package db manages the local development database lifecycle for the Django
// backend (rotkehlchen-web). Migrations are out-of-band (the container entrypoint
// does not migrate), so rweb owns create/migrate/reset and friends.
package db

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/envutil"
	"github.com/kelsos/rweb/internal/secrets"
)

// backupExt is the extension used for custom-format pg_dump files.
const backupExt = ".dump"

// Migrate runs `uv run manage.py migrate`.
func Migrate(cfg *config.Config, st *secrets.Store) error {
	return Manage(cfg, st, "migrate")
}

// Superuser runs the interactive createsuperuser command.
func Superuser(cfg *config.Config, st *secrets.Store) error {
	return Manage(cfg, st, "createsuperuser")
}

// Shell opens the Django dbshell.
func Shell(cfg *config.Config, st *secrets.Store) error {
	return Manage(cfg, st, "dbshell")
}

// Manage runs an arbitrary `uv run manage.py <args>` in rotkehlchen-web with the
// repo env files loaded and the django secret scope overlaid on top.
func Manage(cfg *config.Config, st *secrets.Store, args ...string) error {
	if cfg.Repos.RotkehlchenWeb == "" {
		return fmt.Errorf("repos.rotkehlchen_web not set in config")
	}
	djEnv, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		return err
	}
	c := exec.Command(cfg.Tools.UV, append([]string{"run", "manage.py"}, args...)...)
	c.Dir = cfg.Repos.RotkehlchenWeb
	c.Env = envutil.Compose(djangoEnvFiles(cfg), djEnv)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// Create creates the postgres role and database via `docker compose exec postgres`.
func Create(cfg *config.Config, st *secrets.Store) error {
	pgPass, dbPass, err := dbPasswords(st)
	if err != nil {
		return err
	}
	stmts := []string{
		fmt.Sprintf("CREATE USER %s PASSWORD '%s'", cfg.DB.User, dbPass),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", cfg.DB.Name, cfg.DB.User),
	}
	for _, stmt := range stmts {
		if err := composePsql(cfg, pgPass, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Reset drops and recreates the database, then migrates (fresh empty DB for e2e).
func Reset(cfg *config.Config, st *secrets.Store) error {
	pgPass, _, err := dbPasswords(st)
	if err != nil {
		return err
	}
	_ = composePsql(cfg, pgPass, fmt.Sprintf("DROP DATABASE IF EXISTS %s", cfg.DB.Name))
	if err := composePsql(cfg, pgPass, fmt.Sprintf("CREATE DATABASE %s OWNER %s", cfg.DB.Name, cfg.DB.User)); err != nil {
		return err
	}
	return Migrate(cfg, st)
}

// BackupInfo describes a database dump found in the backups directory.
type BackupInfo struct {
	Name    string    // file name without the .dump extension
	Path    string    // absolute path to the dump
	Size    int64     // size in bytes
	ModTime time.Time // when the dump was written
}

// Backup writes a compressed, custom-format pg_dump of the development database
// into the backups directory and returns the path written. If name is empty a
// timestamped name is generated. The dump is written atomically (temp + rename)
// so a failed dump never leaves a half-written file behind.
func Backup(cfg *config.Config, st *secrets.Store, name string) (string, error) {
	pgPass, _, err := dbPasswords(st)
	if err != nil {
		return "", err
	}
	if cfg.Repos.RotkehlchenWeb == "" {
		return "", fmt.Errorf("repos.rotkehlchen_web not set in config")
	}
	dir := cfg.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if name == "" {
		name = cfg.DB.Name + "-" + time.Now().Format("20060102-150405")
	}
	dest := filepath.Join(dir, strings.TrimSuffix(name, backupExt)+backupExt)

	tmp, err := os.CreateTemp(dir, ".bak-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	c := exec.Command(cfg.Tools.Docker, "compose", "exec", "-T", "postgres",
		"pg_dump", "-U", "postgres", "-Fc", cfg.DB.Name)
	c.Dir = cfg.Repos.RotkehlchenWeb
	c.Env = envutil.Compose(nil, map[string]string{"PGPASSWORD": pgPass, "POSTGRES_PASSWORD": pgPass})
	c.Stdout = tmp
	c.Stderr = os.Stderr
	runErr := c.Run()
	closeErr := tmp.Close()
	if runErr != nil {
		return "", fmt.Errorf("pg_dump: %w", runErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Restore replaces the development database contents from a backup. ref may be a
// path, a bare backup name, or "latest". It is destructive: existing objects are
// dropped (pg_restore --clean) before the dump is loaded. The database must
// already exist (run `rweb db create` first).
func Restore(cfg *config.Config, st *secrets.Store, ref string) error {
	pgPass, _, err := dbPasswords(st)
	if err != nil {
		return err
	}
	if cfg.Repos.RotkehlchenWeb == "" {
		return fmt.Errorf("repos.rotkehlchen_web not set in config")
	}
	path, err := ResolveBackup(cfg, ref)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	c := exec.Command(cfg.Tools.Docker, "compose", "exec", "-T", "postgres",
		"pg_restore", "-U", "postgres", "-d", cfg.DB.Name, "--clean", "--if-exists", "--no-acl")
	c.Dir = cfg.Repos.RotkehlchenWeb
	c.Env = envutil.Compose(nil, map[string]string{"PGPASSWORD": pgPass, "POSTGRES_PASSWORD": pgPass})
	c.Stdin = f
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	return nil
}

// ListBackups returns the dumps in the backups directory, newest first. A
// missing directory is not an error: it simply yields no backups.
func ListBackups(cfg *config.Config) ([]BackupInfo, error) {
	dir := cfg.BackupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), backupExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Name:    strings.TrimSuffix(e.Name(), backupExt),
			Path:    filepath.Join(dir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// ResolveBackup turns a user-supplied reference into an absolute dump path. ref
// may be "" or "latest" (newest dump in the backups dir), a path containing a
// separator (used as-is), or a bare backup name looked up in the backups dir.
func ResolveBackup(cfg *config.Config, ref string) (string, error) {
	if ref == "" || ref == "latest" {
		backups, err := ListBackups(cfg)
		if err != nil {
			return "", err
		}
		if len(backups) == 0 {
			return "", fmt.Errorf("no backups found in %s", cfg.BackupDir())
		}
		return backups[0].Path, nil
	}
	if strings.ContainsRune(ref, filepath.Separator) {
		if _, err := os.Stat(ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	cand := filepath.Join(cfg.BackupDir(), strings.TrimSuffix(ref, backupExt)+backupExt)
	if _, err := os.Stat(cand); err != nil {
		return "", fmt.Errorf("backup %q not found in %s", ref, cfg.BackupDir())
	}
	return cand, nil
}

func djangoEnvFiles(cfg *config.Config) []string {
	return []string{
		filepath.Join(cfg.Repos.RotkehlchenWeb, "localtest_env"),
		filepath.Join(cfg.Repos.RotkehlchenWeb, ".env"),
	}
}

func dbPasswords(st *secrets.Store) (pgPass, dbPass string, err error) {
	shared, err := st.EnvFor(secrets.ScopeShared)
	if err != nil {
		return "", "", err
	}
	dj, err := st.EnvFor(secrets.ScopeDjango)
	if err != nil {
		return "", "", err
	}
	pgPass = shared["POSTGRES_PASSWORD"]
	dbPass = dj["DB_PASS"]
	if pgPass == "" {
		return "", "", fmt.Errorf("POSTGRES_PASSWORD missing from [shared] secrets")
	}
	if dbPass == "" {
		return "", "", fmt.Errorf("DB_PASS missing from [django] secrets")
	}
	return pgPass, dbPass, nil
}

func composePsql(cfg *config.Config, pgPass, stmt string) error {
	if cfg.Repos.RotkehlchenWeb == "" {
		return fmt.Errorf("repos.rotkehlchen_web not set in config")
	}
	c := exec.Command(cfg.Tools.Docker, "compose", "exec", "-T", "postgres",
		"psql", "-U", "postgres", "-c", stmt)
	c.Dir = cfg.Repos.RotkehlchenWeb
	c.Env = envutil.Compose(nil, map[string]string{"PGPASSWORD": pgPass, "POSTGRES_PASSWORD": pgPass})
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
