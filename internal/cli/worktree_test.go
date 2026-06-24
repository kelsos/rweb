package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kelsos/rweb/internal/config"
)

// repoWithWorktree creates a git repo and a linked "feature/x" worktree, or
// skips when git is unavailable.
func repoWithWorktree(t *testing.T) (main, linked string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	main = filepath.Join(root, "main")
	run := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("", "init", "-b", "main", main)
	run(main, "config", "user.email", "t@example.com")
	run(main, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", "f")
	run(main, "commit", "-m", "init")
	linked = filepath.Join(root, "feature")
	run(main, "worktree", "add", "-b", "feature/x", linked)
	return main, linked
}

func TestApplyWorktrees_StickyAndFlag(t *testing.T) {
	main, linked := repoWithWorktree(t)

	// Sticky selection resolves the repo path to the linked worktree.
	cfg := &config.Config{Worktrees: map[string]string{"rotki_com": "feature/x"}}
	cfg.Repos.RotkiCom = main
	if err := applyWorktrees(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkiCom != linked {
		t.Fatalf("sticky: RotkiCom = %q, want %q", cfg.Repos.RotkiCom, linked)
	}

	// A --worktree flag overrides the sticky selection.
	cfg = &config.Config{Worktrees: map[string]string{"rotki_com": "feature/x"}}
	cfg.Repos.RotkiCom = main
	if err := applyWorktrees(cfg, []string{"rotki_com=main"}); err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkiCom != main {
		t.Fatalf("flag override: RotkiCom = %q, want %q", cfg.Repos.RotkiCom, main)
	}
}

func TestApplyWorktrees_MissingFallsBack(t *testing.T) {
	main, _ := repoWithWorktree(t)
	cfg := &config.Config{Worktrees: map[string]string{"rotki_com": "does-not-exist"}}
	cfg.Repos.RotkiCom = main
	if err := applyWorktrees(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkiCom != main {
		t.Fatalf("missing worktree should fall back to %q, got %q", main, cfg.Repos.RotkiCom)
	}
}

func TestApplyWorktrees_Errors(t *testing.T) {
	cfg := &config.Config{}
	cfg.Repos.RotkiCom = "/tmp"
	if err := applyWorktrees(cfg, []string{"rotki_com"}); err == nil {
		t.Error("malformed --worktree (no =) should error")
	}
	if err := applyWorktrees(cfg, []string{"bogus=main"}); err == nil {
		t.Error("unknown repo key should error")
	}
}

func TestApplyWorktrees_NoSelectionNoop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Repos.RotkiCom = "/some/path"
	cfg.Repos.RotkehlchenWeb = "/another/path"
	if err := applyWorktrees(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkiCom != "/some/path" || cfg.Repos.RotkehlchenWeb != "/another/path" {
		t.Error("no selection must leave repo paths untouched (no git invoked)")
	}
}
