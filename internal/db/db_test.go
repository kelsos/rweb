package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kelsos/rweb/internal/config"
)

// writeBackup creates a fake dump file with a controlled mod time.
func writeBackup(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfgWithBackups(dir string) *config.Config {
	cfg := config.Default()
	cfg.DB.BackupDir = dir
	return cfg
}

func TestBackupDirOverride(t *testing.T) {
	cfg := config.Default()
	if cfg.BackupDir() == "" {
		t.Fatal("default BackupDir should be non-empty")
	}
	cfg.DB.BackupDir = "/tmp/custom-backups"
	if got := cfg.BackupDir(); got != "/tmp/custom-backups" {
		t.Fatalf("override not honoured: %q", got)
	}
}

func TestListBackupsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	writeBackup(t, dir, "old.dump", 2*time.Hour)
	writeBackup(t, dir, "new.dump", 1*time.Minute)
	writeBackup(t, dir, "notadump.txt", time.Minute) // ignored
	if err := os.Mkdir(filepath.Join(dir, "sub.dump"), 0o755); err != nil {
		t.Fatal(err) // a directory ending in .dump must be ignored
	}

	got, err := ListBackups(cfgWithBackups(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 backups, got %d: %+v", len(got), got)
	}
	if got[0].Name != "new" || got[1].Name != "old" {
		t.Fatalf("not sorted newest-first: %s, %s", got[0].Name, got[1].Name)
	}
}

func TestListBackupsMissingDir(t *testing.T) {
	got, err := ListBackups(cfgWithBackups(filepath.Join(t.TempDir(), "does-not-exist")))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestResolveBackup(t *testing.T) {
	dir := t.TempDir()
	writeBackup(t, dir, "old.dump", 2*time.Hour)
	newest := writeBackup(t, dir, "new.dump", time.Minute)
	cfg := cfgWithBackups(dir)

	t.Run("latest", func(t *testing.T) {
		got, err := ResolveBackup(cfg, "latest")
		if err != nil || got != newest {
			t.Fatalf("latest = %q, %v; want %q", got, err, newest)
		}
	})
	t.Run("empty is latest", func(t *testing.T) {
		got, err := ResolveBackup(cfg, "")
		if err != nil || got != newest {
			t.Fatalf("empty = %q, %v; want %q", got, err, newest)
		}
	})
	t.Run("bare name", func(t *testing.T) {
		got, err := ResolveBackup(cfg, "old")
		if err != nil || got != filepath.Join(dir, "old.dump") {
			t.Fatalf("bare name = %q, %v", got, err)
		}
	})
	t.Run("bare name with extension", func(t *testing.T) {
		got, err := ResolveBackup(cfg, "old.dump")
		if err != nil || got != filepath.Join(dir, "old.dump") {
			t.Fatalf("bare name.dump = %q, %v", got, err)
		}
	})
	t.Run("explicit path", func(t *testing.T) {
		got, err := ResolveBackup(cfg, newest)
		if err != nil || got != newest {
			t.Fatalf("explicit path = %q, %v", got, err)
		}
	})
	t.Run("unknown name", func(t *testing.T) {
		if _, err := ResolveBackup(cfg, "nope"); err == nil {
			t.Fatal("want error for unknown name")
		}
	})
	t.Run("no backups", func(t *testing.T) {
		empty := cfgWithBackups(t.TempDir())
		if _, err := ResolveBackup(empty, "latest"); err == nil {
			t.Fatal("want error when no backups exist")
		}
	})
}
