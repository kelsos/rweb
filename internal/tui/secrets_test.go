package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kelsos/rweb/internal/secrets"
	"github.com/zalando/go-keyring"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newTestStore(t *testing.T) *secrets.Store {
	t.Helper()
	keyring.MockInit() // in-memory keychain, no host pollution
	dir := t.TempDir()
	st := secrets.New(filepath.Join(dir, "secrets.age"), "rweb-test", "age-identity", filepath.Join(dir, "age.key"), "")
	if _, _, err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return st
}

func TestSecretManagerRevealAndDelete(t *testing.T) {
	st := newTestStore(t)
	if err := st.Set(secrets.ScopeShared, "FOO", "bar"); err != nil {
		t.Fatal(err)
	}

	m, err := newModel(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(m.rows))
	}

	// reveal toggles
	updated, _ := m.Update(key(" "))
	m = updated.(model)
	if !m.reveal["shared/FOO"] {
		t.Fatal("expected secret to be revealed after space")
	}

	// delete: 'd' then confirm 'y'
	updated, _ = m.Update(key("d"))
	updated, _ = updated.(model).Update(key("y"))
	m = updated.(model)
	if len(m.rows) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(m.rows))
	}
	data, _ := st.Read()
	if len(data[secrets.ScopeShared]) != 0 {
		t.Fatalf("expected store to be empty after delete, got %v", data)
	}
}

func TestSecretManagerFilter(t *testing.T) {
	st := newTestStore(t)
	must(t, st.Set(secrets.ScopeShared, "REDIS_HOST", "x"))
	must(t, st.Set(secrets.ScopeShared, "REDIS_PASSWORD", "y"))
	must(t, st.Set(secrets.ScopeDjango, "DB_HOST", "z"))

	m, err := newModel(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(m.rows))
	}

	// '/' then type "redis" -> only the two REDIS_* rows remain
	updated, _ := m.Update(key("/"))
	for _, c := range []string{"r", "e", "d", "i", "s"} {
		updated, _ = updated.(model).Update(key(c))
	}
	m = updated.(model)
	if len(m.rows) != 2 {
		t.Fatalf("expected 2 filtered rows, got %d", len(m.rows))
	}

	// esc clears the filter
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.filter != "" || len(m.rows) != 3 {
		t.Fatalf("expected filter cleared and 3 rows, got filter=%q rows=%d", m.filter, len(m.rows))
	}
}

func TestSecretManagerRevealAll(t *testing.T) {
	st := newTestStore(t)
	must(t, st.Set(secrets.ScopeShared, "FOO", "bar"))

	m, err := newModel(st)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(key("R"))
	m = updated.(model)
	if !m.revealAll {
		t.Fatal("expected revealAll after R")
	}
	if !strings.Contains(m.View(), "FOO = bar") {
		t.Fatal("expected revealed value in view")
	}
}

func TestSecretManagerAddFlow(t *testing.T) {
	st := newTestStore(t)
	m, err := newModel(st)
	if err != nil {
		t.Fatal(err)
	}

	// a -> scope (default shared) -> key "DB_HOST" -> value "localhost"
	step := func(mod tea.Model, msgs ...tea.KeyMsg) tea.Model {
		for _, msg := range msgs {
			mod, _ = mod.Update(msg)
		}
		return mod
	}
	mod := step(m, key("a"))                        // enter add flow (scope prefilled)
	mod = step(mod, tea.KeyMsg{Type: tea.KeyEnter}) // accept scope "shared"
	mod = step(mod, key("D"), key("B"), key("_"), key("H"), key("O"), key("S"), key("T"))
	mod = step(mod, tea.KeyMsg{Type: tea.KeyEnter}) // key set
	mod = step(mod, key("l"), key("o"), key("c"), key("a"), key("l"))
	mod = step(mod, tea.KeyMsg{Type: tea.KeyEnter}) // value committed

	final := mod.(model)
	if final.mode != modeList {
		t.Fatalf("expected to return to list mode, got %v", final.mode)
	}
	data, _ := st.Read()
	if got := data[secrets.ScopeShared]["DB_HOST"]; got != "local" {
		t.Fatalf("expected DB_HOST=local, got %q", got)
	}
}
