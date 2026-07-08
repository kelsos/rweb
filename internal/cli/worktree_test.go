package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kelsos/rweb/internal/config"
	"github.com/spf13/cobra"
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

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestCompleteRepoKeys(t *testing.T) {
	withTempConfig(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	// The first positional arg is completed with the configured repo keys.
	names, directive := completeRepoKeys(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !contains(names, "rotki_com") || !contains(names, "rotkehlchen_web") {
		t.Errorf("expected repo keys among completions, got %v", names)
	}

	// Once the repo arg is supplied there is nothing more to complete.
	if names, _ := completeRepoKeys(nil, []string{"rotki_com"}, ""); names != nil {
		t.Errorf("no completion expected past the first arg, got %v", names)
	}
}

func TestCompleteRepoKeys_NoConfig(t *testing.T) {
	withTempConfig(t) // temp XDG dir, but nothing saved: config.Load fails
	names, directive := completeRepoKeys(nil, nil, "")
	if names != nil {
		t.Errorf("missing config should yield no completions, got %v", names)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

func TestCompleteWorktreeNames(t *testing.T) {
	main, _ := repoWithWorktree(t)
	withTempConfig(t)
	cfg := config.Default()
	cfg.Repos.RotkiCom = main
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	names, directive := completeWorktreeNames(nil, []string{"rotki_com"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	// Both the branch name and the linked worktree's dir base name are offered.
	if !contains(names, "main") || !contains(names, "feature/x") || !contains(names, "feature") {
		t.Errorf("expected branch and dir-base completions, got %v", names)
	}
}

func TestCompleteWorktreeNames_Guards(t *testing.T) {
	withTempConfig(t)
	cfg := config.Default()
	cfg.Repos.RotkiCom = "/does/not/exist"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Wrong arg count (the repo isn't chosen yet, or a second name is being typed).
	if names, _ := completeWorktreeNames(nil, nil, ""); names != nil {
		t.Errorf("no completion without the repo arg, got %v", names)
	}
	// Unknown repo key.
	if names, _ := completeWorktreeNames(nil, []string{"bogus"}, ""); names != nil {
		t.Errorf("unknown repo should yield no completions, got %v", names)
	}
	// Known repo whose path is not a git work tree.
	if names, _ := completeWorktreeNames(nil, []string{"rotki_com"}, ""); names != nil {
		t.Errorf("non-worktree path should yield no completions, got %v", names)
	}
}
