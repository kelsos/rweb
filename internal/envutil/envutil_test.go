package envutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "env")
	content := "# comment\n\nexport DB_HOST=localhost\nDB_PORT=5432\nQUOTED=\"a b\"\nSINGLE='c d'\nBAD_LINE\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432", "QUOTED": "a b", "SINGLE": "c d"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s: got %q want %q", k, m[k], v)
		}
	}
	if _, ok := m["BAD_LINE"]; ok {
		t.Error("BAD_LINE should be ignored")
	}
}

func TestComposeOverlayWins(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "env")
	if err := os.WriteFile(p, []byte("FOO=fromfile\nBAR=keepme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := Compose([]string{p, filepath.Join(dir, "missing")}, map[string]string{"FOO": "override"})
	got := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				got[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if got["FOO"] != "override" {
		t.Errorf("overlay should win: got FOO=%q", got["FOO"])
	}
	if got["BAR"] != "keepme" {
		t.Errorf("file value should remain: got BAR=%q", got["BAR"])
	}
}

func TestComposeWithBasePrecedence(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "env")
	// File sets FROMFILE and overrides OVERRIDDEN; base also provides ONLYBASE.
	if err := os.WriteFile(p, []byte("FROMFILE=file\nOVERRIDDEN=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"ONLYBASE": "base", "OVERRIDDEN": "base"}
	env := ComposeWithBase(base, []string{p}, map[string]string{"OVERRIDDEN": "overlay"})
	got := map[string]string{}
	for _, kv := range env {
		if i := indexEq(kv); i >= 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	// base value survives when nothing else sets it (the fresh-checkout case).
	if got["ONLYBASE"] != "base" {
		t.Errorf("base-only key should survive: got ONLYBASE=%q", got["ONLYBASE"])
	}
	// file overrides base; overlay overrides file: os.Environ < base < file < overlay.
	if got["OVERRIDDEN"] != "overlay" {
		t.Errorf("precedence base<file<overlay broken: got OVERRIDDEN=%q", got["OVERRIDDEN"])
	}
	if got["FROMFILE"] != "file" {
		t.Errorf("file value should remain: got FROMFILE=%q", got["FROMFILE"])
	}
}

func indexEq(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return i
		}
	}
	return -1
}
