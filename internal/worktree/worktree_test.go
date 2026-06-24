package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseList(t *testing.T) {
	out := []byte(`worktree /repos/rotki.com/main
HEAD 8f0f2d7ac6c37cd317ad66ac7f9a86429caa9882
branch refs/heads/main

worktree /repos/rotki.com/develop
HEAD 40c13fd11d3b5595cf5e698d2cad375cf2a27916
branch refs/heads/develop

worktree /repos/rotki.com/feature-reddit-link
HEAD 8f0f2d7ac6c37cd317ad66ac7f9a86429caa9882
branch refs/heads/feature/reddit-link

worktree /repos/rotki.com/detached
HEAD 1111111111111111111111111111111111111111
detached
`)
	trees := parseList(out)
	if len(trees) != 4 {
		t.Fatalf("got %d trees, want 4: %+v", len(trees), trees)
	}
	want := []Tree{
		{"/repos/rotki.com/main", "main"},
		{"/repos/rotki.com/develop", "develop"},
		{"/repos/rotki.com/feature-reddit-link", "feature/reddit-link"},
		{"/repos/rotki.com/detached", ""},
	}
	for i, w := range want {
		if trees[i] != w {
			t.Errorf("tree[%d] = %+v, want %+v", i, trees[i], w)
		}
	}
}

// withRepo builds a temporary git repo plus a linked worktree and returns both
// paths, skipping the test when git is unavailable.
func withRepo(t *testing.T) (main, linked string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	main = filepath.Join(root, "main")
	mustGit(t, "", "init", "-b", "main", main)
	mustGit(t, main, "config", "user.email", "t@example.com")
	mustGit(t, main, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, main, "add", "f")
	mustGit(t, main, "commit", "-m", "init")
	linked = filepath.Join(root, "feature")
	mustGit(t, main, "worktree", "add", "-b", "feature/x", linked)
	return main, linked
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestIsWorkTree(t *testing.T) {
	main, _ := withRepo(t)
	if !IsWorkTree(main) {
		t.Error("main checkout should be a work tree")
	}
	// The parent directory contains the repo but is not itself a work tree —
	// this is exactly the container-dir footgun doctor must reject.
	if IsWorkTree(filepath.Dir(main)) {
		t.Error("container dir must not be reported as a work tree")
	}
	if IsWorkTree("") {
		t.Error("empty path is not a work tree")
	}
}

func TestResolve(t *testing.T) {
	main, linked := withRepo(t)

	// Empty selection: keep configured path, matched=false.
	got, matched, err := Resolve(main, "")
	if err != nil || matched || got != main {
		t.Fatalf("empty sel = (%q, %v, %v), want (%q, false, nil)", got, matched, err, main)
	}

	// Select by branch name.
	got, matched, err = Resolve(main, "feature/x")
	if err != nil || !matched || got != linked {
		t.Fatalf("branch sel = (%q, %v, %v), want (%q, true, nil)", got, matched, err, linked)
	}

	// Select by worktree directory base name.
	got, matched, err = Resolve(main, "feature")
	if err != nil || !matched || got != linked {
		t.Fatalf("basename sel = (%q, %v, %v), want (%q, true, nil)", got, matched, err, linked)
	}

	// Resolving from the linked worktree finds the main one too.
	got, matched, err = Resolve(linked, "main")
	if err != nil || !matched || got != main {
		t.Fatalf("from-leaf sel = (%q, %v, %v), want (%q, true, nil)", got, matched, err, main)
	}

	// Unknown selection falls back to the configured path, matched=false.
	got, matched, err = Resolve(main, "nope")
	if err != nil || matched || got != main {
		t.Fatalf("missing sel = (%q, %v, %v), want (%q, false, nil)", got, matched, err, main)
	}

	// A non-repository path is an error.
	if _, _, err := Resolve(filepath.Dir(main), "main"); err == nil {
		t.Error("expected error resolving against a non-work-tree path")
	}
}
