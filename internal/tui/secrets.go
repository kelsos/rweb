// Package tui contains rweb's bubbletea terminal interfaces.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kelsos/rweb/internal/secrets"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	scopeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("13"))
	faintStyle  = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeAddScope
	modeAddKey
	modeAddValue
	modeEditValue
	modeConfirmDelete
)

type row struct{ scope, key string }

func rowID(r row) string { return r.scope + "/" + r.key }

type model struct {
	store *secrets.Store
	data  map[string]map[string]string

	rows   []row // filtered, display order
	cursor int

	reveal    map[string]bool
	revealAll bool
	filter    string

	mode  mode
	input textinput.Model

	scopeChoices []string
	scopeCursor  int

	newScope string
	newKey   string
	editing  row

	status string
	err    error
}

// RunSecrets launches the interactive secret manager against the given store.
func RunSecrets(st *secrets.Store) error {
	m, err := newModel(st)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func newModel(st *secrets.Store) (model, error) {
	data, err := st.Read()
	if err != nil {
		return model{}, err
	}
	ti := textinput.New()
	ti.CharLimit = 1024
	ti.Prompt = "> "
	m := model{
		store:        st,
		data:         data,
		reveal:       map[string]bool{},
		input:        ti,
		mode:         modeList,
		scopeChoices: secrets.Scopes(),
	}
	m.rebuild()
	return m, nil
}

func (m *model) rebuild() {
	var rows []row
	seen := map[string]bool{}
	add := func(scope string) {
		for _, k := range sortedKeys(m.data[scope]) {
			if m.matches(scope, k) {
				rows = append(rows, row{scope, k})
			}
		}
		seen[scope] = true
	}
	for _, scope := range secrets.Scopes() {
		add(scope)
	}
	for scope := range m.data { // any non-canonical scopes
		if !seen[scope] {
			add(scope)
		}
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m model) matches(scope, key string) bool {
	if m.filter == "" {
		return true
	}
	f := strings.ToLower(m.filter)
	return strings.Contains(strings.ToLower(key), f) || strings.Contains(strings.ToLower(scope), f)
}

func (m *model) reload() {
	d, err := m.store.Read()
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.data = d
	m.rebuild()
}

func (m model) curRow() (row, bool) {
	if len(m.rows) == 0 || m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

func (m model) currentScopeOr(def string) string {
	if r, ok := m.curRow(); ok {
		return r.scope
	}
	return def
}

func (m model) missingCount() int {
	n := 0
	for _, scope := range secrets.Scopes() {
		have := m.data[scope]
		for _, req := range secrets.Manifest[scope] {
			if _, ok := have[req]; !ok {
				n++
			}
		}
	}
	return n
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.mode {
	case modeList:
		return m.updateList(key)
	case modeFilter:
		return m.updateFilter(key)
	case modeAddScope:
		return m.updateScopePick(key)
	case modeConfirmDelete:
		return m.updateConfirm(key)
	default:
		return m.updateInput(key)
	}
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case " ", "space":
		if r, ok := m.curRow(); ok {
			id := rowID(r)
			m.reveal[id] = !m.reveal[id]
		}
	case "R":
		m.revealAll = !m.revealAll
	case "/":
		m.mode = modeFilter
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "filter by key/scope"
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		m.input.Focus()
		m.status = ""
		return m, textinput.Blink
	case "a":
		m.mode = modeAddScope
		m.scopeCursor = indexOf(m.scopeChoices, m.currentScopeOr("shared"))
		m.status = "add → choose scope"
	case "e":
		if r, ok := m.curRow(); ok {
			m.editing = r
			m.mode = modeEditValue
			m.input.EchoMode = textinput.EchoPassword
			m.input.Placeholder = "new value"
			m.input.SetValue("")
			m.input.Focus()
			m.status = "edit " + rowID(r)
			return m, textinput.Blink
		}
	case "d":
		if r, ok := m.curRow(); ok {
			m.mode = modeConfirmDelete
			m.status = "delete " + rowID(r) + "? (y/n)"
		}
	}
	return m, nil
}

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter = ""
		m.input.Blur()
		m.mode = modeList
		m.rebuild()
		return m, nil
	case "enter":
		m.input.Blur()
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filter = m.input.Value()
	m.rebuild()
	return m, cmd
}

func (m model) updateScopePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.mode = modeList
		m.status = "cancelled"
	case "up", "k":
		if m.scopeCursor > 0 {
			m.scopeCursor--
		}
	case "down", "j":
		if m.scopeCursor < len(m.scopeChoices)-1 {
			m.scopeCursor++
		}
	case "enter":
		m.newScope = m.scopeChoices[m.scopeCursor]
		m.mode = modeAddKey
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = "KEY"
		m.input.SetValue("")
		m.input.Focus()
		m.status = "add → key (" + m.newScope + ")"
		return m, textinput.Blink
	}
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if s := msg.String(); s == "y" || s == "Y" {
		if r, ok := m.curRow(); ok {
			if err := m.store.Rm(r.scope, r.key); err != nil {
				m.err = err
			} else {
				m.status = "deleted " + rowID(r)
				m.reload()
			}
		}
	} else {
		m.status = "cancelled"
	}
	m.mode = modeList
	return m, nil
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.status = "cancelled"
		return m, nil
	case "enter":
		return m.commit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) commit() (tea.Model, tea.Cmd) {
	val := m.input.Value()
	switch m.mode {
	case modeAddKey:
		k := strings.TrimSpace(val)
		if k == "" {
			m.status = "key is required"
			return m, nil
		}
		m.newKey = k
		m.mode = modeAddValue
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "value (hidden)"
		m.input.SetValue("")
		m.status = "add → value (" + m.newScope + "/" + m.newKey + ")"
		return m, textinput.Blink
	case modeAddValue:
		if err := m.store.Set(m.newScope, m.newKey, val); err != nil {
			m.err = err
		} else {
			m.status = "added " + m.newScope + "/" + m.newKey
			m.reload()
		}
		m.mode = modeList
		m.input.Blur()
	case modeEditValue:
		if err := m.store.Set(m.editing.scope, m.editing.key, val); err != nil {
			m.err = err
		} else {
			m.status = "updated " + rowID(m.editing)
			m.reload()
		}
		m.mode = modeList
		m.input.Blur()
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("rweb · secret manager") + "\n")

	// Status header: encryption note, missing-required count, filter, reveal-all.
	sub := []string{"age-encrypted · values masked by default"}
	if n := m.missingCount(); n > 0 {
		sub = append(sub, warnStyle.Render(fmt.Sprintf("%d required missing", n)))
	} else {
		sub = append(sub, okStyle.Render("all required present"))
	}
	if m.revealAll {
		sub = append(sub, warnStyle.Render("REVEAL ALL"))
	}
	if m.filter != "" {
		sub = append(sub, "filter: "+m.filter)
	}
	b.WriteString(faintStyle.Render(strings.Join(sub, " · ")) + "\n\n")

	if m.mode == modeAddScope {
		b.WriteString("Choose scope:\n")
		for i, s := range m.scopeChoices {
			if i == m.scopeCursor {
				b.WriteString(cursorStyle.Render("› "+s) + "\n")
			} else {
				b.WriteString("  " + s + "\n")
			}
		}
		b.WriteString("\n" + faintStyle.Render("↑/↓ choose · enter select · esc cancel") + "\n")
		return b.String()
	}

	if len(m.rows) == 0 {
		if m.filter != "" {
			b.WriteString(faintStyle.Render("  (no matches)") + "\n")
		} else {
			b.WriteString(faintStyle.Render("  (no secrets — press 'a' to add)") + "\n")
		}
	}
	curScope := ""
	for i, r := range m.rows {
		if r.scope != curScope {
			b.WriteString(scopeStyle.Render("["+r.scope+"]") + "\n")
			curScope = r.scope
		}
		val := "••••"
		if m.revealAll || m.reveal[rowID(r)] {
			val = m.data[r.scope][r.key]
		}
		line := fmt.Sprintf("%s = %s", r.key, val)
		if i == m.cursor && m.mode == modeList {
			b.WriteString(cursorStyle.Render("› "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}

	b.WriteString("\n")
	switch m.mode {
	case modeFilter:
		b.WriteString(m.input.View() + "\n")
		b.WriteString(faintStyle.Render("type to filter · enter apply · esc clear") + "\n")
		return b.String()
	case modeAddKey, modeAddValue, modeEditValue:
		b.WriteString(m.input.View() + "\n")
	}

	if m.err != nil {
		b.WriteString(errStyle.Render("error: "+m.err.Error()) + "\n")
	}
	if m.status != "" {
		b.WriteString(faintStyle.Render(m.status) + "\n")
	}
	b.WriteString(faintStyle.Render("↑/↓ move · space reveal · R reveal-all · / filter · a add · e edit · d delete · q quit") + "\n")
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return 0
}
