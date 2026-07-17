package cli

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// The Playwright suites start their own Nuxt dev server, so they need the same
// macOS TMPDIR fix as the nuxt service: the per-user /var/folders/... path
// overruns the 104-byte unix socket limit once vite-node appends its socket
// name. The override must come after os.Environ to win, since exec resolves
// duplicate keys to the last occurrence.
func TestE2EEnvTmpdir(t *testing.T) {
	long := "/var/folders/zn/n9fpvbnj3fn971b5s466fzd40000gn/T/"
	t.Setenv("TMPDIR", long)

	last := ""
	for _, kv := range e2eEnv() {
		if strings.HasPrefix(kv, "TMPDIR=") {
			last = strings.TrimPrefix(kv, "TMPDIR=")
		}
	}

	want := long
	if runtime.GOOS == "darwin" {
		want = "/tmp"
	}
	if last != want {
		t.Errorf("e2eEnv TMPDIR = %q, want %q", last, want)
	}
}

// e2eEnv replaces the inherited env rather than adding to it, so the rest of the
// ambient environment must survive: pnpm needs PATH, and Playwright reads HOME.
func TestE2EEnvKeepsAmbient(t *testing.T) {
	t.Setenv("RWEB_E2E_CANARY", "kept")
	got := e2eEnv()
	if len(got) < len(os.Environ()) {
		t.Errorf("e2eEnv dropped entries: got %d, os.Environ has %d", len(got), len(os.Environ()))
	}
	var found bool
	for _, kv := range got {
		if kv == "RWEB_E2E_CANARY=kept" {
			found = true
		}
	}
	if !found {
		t.Error("e2eEnv should preserve the ambient environment")
	}
}
