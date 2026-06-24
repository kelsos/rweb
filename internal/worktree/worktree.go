// Package worktree resolves a configured repository path to a specific git
// working tree. It treats git as the source of truth — worktrees are discovered
// at runtime via `git worktree list` rather than modelled in config — so the
// same logic works whether a repo is a plain checkout or one leaf of a
// multi-worktree layout (e.g. rotki.com/{main,develop,feature-*}).
package worktree

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tree is one linked working tree of a git repository.
type Tree struct {
	Path   string // absolute path to the working tree
	Branch string // short branch name (e.g. "develop", "feature/reddit-link"); empty if detached
}

// IsWorkTree reports whether path is inside a git working tree. It rejects
// non-repository directories — including a container directory that merely holds
// worktrees as subdirectories (that dir is not itself a work tree).
func IsWorkTree(path string) bool {
	if path == "" {
		return false
	}
	out, err := exec.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// List returns every working tree linked to the repository that path belongs to,
// via `git worktree list --porcelain`. It yields identical results whether path
// is the main checkout or any linked worktree.
func List(path string) ([]Tree, error) {
	out, err := exec.Command("git", "-C", path, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", path, err)
	}
	return parseList(out), nil
}

// parseList parses `git worktree list --porcelain` output. Each record starts
// with a "worktree <path>" line and ends at a blank line; "branch <ref>" carries
// the checked-out branch (absent when detached).
func parseList(out []byte) []Tree {
	var trees []Tree
	var cur Tree
	flush := func() {
		if cur.Path != "" {
			trees = append(trees, cur)
		}
		cur = Tree{}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return trees
}

// Resolve maps a worktree selection to a working directory. configured must be a
// valid git working tree. sel may name a branch ("develop", "feature/reddit-link")
// or a worktree directory's base name. When sel is empty, or names a worktree
// this repository does not have, configured is returned with matched=false so
// callers fall back to the configured default.
func Resolve(configured, sel string) (dir string, matched bool, err error) {
	if !IsWorkTree(configured) {
		return "", false, fmt.Errorf("%s is not a git work tree", configured)
	}
	if sel == "" {
		return configured, false, nil
	}
	trees, err := List(configured)
	if err != nil {
		return "", false, err
	}
	for _, t := range trees {
		if t.Branch == sel || filepath.Base(t.Path) == sel {
			return t.Path, true, nil
		}
	}
	return configured, false, nil
}
