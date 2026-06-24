package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kelsos/rweb/internal/config"
)

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, _ := expandPath(""); got != "" {
		t.Errorf("empty path should stay empty, got %q", got)
	}
	if got, _ := expandPath("~"); got != home {
		t.Errorf("~ should expand to home: %q", got)
	}
	if got, _ := expandPath("~/repos/web"); got != filepath.Join(home, "repos/web") {
		t.Errorf("~/ expansion wrong: %q", got)
	}
	got, err := expandPath("relative/dir")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("relative path should become absolute: %q", got)
	}
}

func TestConfigSetRepo(t *testing.T) {
	withTempConfig(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	// Unknown key errors without writing.
	bad := configSetRepoCmd()
	if err := bad.RunE(bad, []string{"bogus", "/x"}); err == nil {
		t.Error("unknown repo key should error")
	}

	// A valid key is set and expanded to absolute.
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd := configSetRepoCmd()
	if err := cmd.RunE(cmd, []string{"rotki_com", "~/repos/com"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkiCom != filepath.Join(home, "repos/com") {
		t.Errorf("rotki_com not set/expanded: %q", cfg.Repos.RotkiCom)
	}

	// Empty path clears an optional repo.
	clr := configSetRepoCmd()
	if err := clr.RunE(clr, []string{"nest", ""}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load()
	if cfg.Repos.Nest != "" {
		t.Errorf("nest should be cleared, got %q", cfg.Repos.Nest)
	}
}

func TestConfigEditRequiresConfig(t *testing.T) {
	withTempConfig(t)
	// No config saved yet → edit should refuse.
	cmd := configEditCmd()
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("edit should error when no config exists")
	}
	// With a config and a no-op EDITOR, it should succeed.
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", "true") // /usr/bin/true: exits 0, ignores args
	if _, err := os.Stat("/usr/bin/true"); err == nil {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Errorf("edit with no-op EDITOR should succeed: %v", err)
		}
	}
}
