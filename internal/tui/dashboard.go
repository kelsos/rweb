package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/proc"
	"github.com/kelsos/rweb/internal/secrets"
)

// maxLogLines bounds the dashboard's in-memory scrollback. The full history is
// always on disk (`rweb logs <svc>`); this is just what you can scroll here.
const maxLogLines = 5000

// hScrollStep is how many columns left/right move the log view horizontally.
const hScrollStep = 12

var (
	healthyState  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	startingState = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	stoppedState  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	logTagStyle   = lipgloss.NewStyle().Bold(true)
	logPalette    = []lipgloss.Color{"10", "12", "13", "14", "11", "9", "6", "5"}
)

type (
	tickMsg       struct{}
	logMsg        proc.LogLine
	logsClosedMsg struct{}
	actionMsg     struct{ status string }
)

type focus int

const (
	focusTable focus = iota
	focusLogs
)

type dashboard struct {
	cfg   *config.Config
	store *secrets.Store

	profile string
	rows    []proc.ServiceStatus
	cursor  int

	logCh     <-chan proc.LogLine
	logTag    map[string]lipgloss.Style
	raw       []proc.LogLine // captured lines (filtered/rendered on demand)
	filterSvc string         // when set, only this service's lines are shown
	xOffset   int            // horizontal scroll offset (columns)
	vp        viewport.Model

	focus  focus
	busy   bool
	status string
	width  int
	height int
}

// RunDashboard launches the interactive supervisor dashboard. It follows the
// logs of the active profile's services and offers start/stop/restart controls.
func RunDashboard(cfg *config.Config, store *secrets.Store) error {
	profile, rows := proc.Snapshot(cfg)
	names := make([]string, len(rows))
	tag := map[string]lipgloss.Style{}
	for i, r := range rows {
		names[i] = r.Name
		tag[r.Name] = logTagStyle.Foreground(logPalette[i%len(logPalette)])
	}
	ch, stop := proc.MultiTail(names, true)

	m := &dashboard{
		cfg: cfg, store: store,
		profile: profile, rows: rows,
		logCh: ch, logTag: tag,
		vp:     viewport.New(0, 0),
		status: "ready",
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	stop()
	return err
}

func (m *dashboard) Init() tea.Cmd {
	return tea.Batch(tickCmd(), waitLog(m.logCh))
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func waitLog(ch <-chan proc.LogLine) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-ch
		if !ok {
			return logsClosedMsg{}
		}
		return logMsg(l)
	}
}

func (m *dashboard) selected() (proc.ServiceStatus, bool) {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor], true
	}
	return proc.ServiceStatus{}, false
}

// renderLogs rebuilds the viewport content from the captured lines, applying the
// active service filter and horizontal offset.
func (m *dashboard) renderLogs() {
	m.vp.SetContent(renderLogLines(m.raw, m.filterSvc, m.logTag, m.xOffset, m.vp.Width))
}

// renderLogLines formats captured log lines for display: it keeps only filterSvc
// (when set), tags each line with its colored service name, and applies a
// horizontal column offset with ANSI-aware slicing so styling survives the cut.
func renderLogLines(raw []proc.LogLine, filterSvc string, tags map[string]lipgloss.Style, xOffset, width int) string {
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		if filterSvc != "" && l.Service != filterSvc {
			continue
		}
		line := fmt.Sprintf("%s │ %s", tags[l.Service].Render(l.Service), l.Text)
		if xOffset > 0 && width > 0 {
			line = ansi.Cut(line, xOffset, xOffset+width)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		m.profile, m.rows = proc.Snapshot(m.cfg)
		if m.cursor >= len(m.rows) {
			m.cursor = max(0, len(m.rows)-1)
		}
		return m, tickCmd()

	case logMsg:
		atBottom := m.vp.AtBottom()
		m.raw = append(m.raw, proc.LogLine(msg))
		if len(m.raw) > maxLogLines {
			m.raw = m.raw[len(m.raw)-maxLogLines:]
		}
		m.renderLogs()
		if atBottom {
			m.vp.GotoBottom()
		}
		return m, waitLog(m.logCh)

	case logsClosedMsg:
		return m, nil

	case actionMsg:
		m.busy = false
		m.status = msg.status
		m.profile, m.rows = proc.Snapshot(m.cfg)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *dashboard) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "l":
		if m.focus == focusTable {
			m.focus = focusLogs
		} else {
			m.focus = focusTable
		}
		return m, nil
	case "f":
		// Toggle the log filter to the selected service (off if already set).
		if s, ok := m.selected(); ok {
			if m.filterSvc == s.Name {
				m.filterSvc = ""
			} else {
				m.filterSvc = s.Name
			}
			m.xOffset = 0
			m.renderLogs()
		}
		return m, nil
	}

	if m.focus == focusLogs {
		switch msg.String() {
		case "left", "h":
			if m.xOffset > 0 {
				m.xOffset -= hScrollStep
				if m.xOffset < 0 {
					m.xOffset = 0
				}
				m.renderLogs()
			}
			return m, nil
		case "right":
			m.xOffset += hScrollStep
			m.renderLogs()
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "s":
		return m, m.action("start", func(s proc.ServiceStatus) error {
			if s.State != "stopped" {
				return fmt.Errorf("%s is already running", s.Name)
			}
			return proc.Restart(m.cfg, m.store, s.Name)
		})
	case "r":
		return m, m.action("restart", func(s proc.ServiceStatus) error {
			return proc.Restart(m.cfg, m.store, s.Name)
		})
	case "x":
		return m, m.action("stop", func(s proc.ServiceStatus) error {
			return proc.StopService(m.cfg, s.Name)
		})
	}
	return m, nil
}

// action runs a (blocking) supervisor operation on the selected service off the
// UI goroutine, reporting the outcome back as an actionMsg.
func (m *dashboard) action(verb string, fn func(proc.ServiceStatus) error) tea.Cmd {
	s, ok := m.selected()
	if !ok || m.busy {
		return nil
	}
	m.busy = true
	m.status = fmt.Sprintf("%s %s…", verb, s.Name)
	return func() tea.Msg {
		if err := fn(s); err != nil {
			return actionMsg{status: fmt.Sprintf("%s %s failed: %v", verb, s.Name, err)}
		}
		return actionMsg{status: fmt.Sprintf("%s %s ✓", verb, s.Name)}
	}
}

// layout sizes the log viewport to whatever space the table and chrome leave.
func (m *dashboard) layout() {
	chrome := len(m.rows) + 7 // title + blank + header + rows + blank + logs-title + help
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	m.vp.Width = m.width
	m.vp.Height = h
}

func (m *dashboard) View() string {
	m.layout()
	var b strings.Builder

	title := titleStyle.Render("rweb dashboard")
	prof := faintStyle.Render(" — profile: " + m.profile)
	b.WriteString(title + prof + "\n\n")

	b.WriteString(faintStyle.Render(fmt.Sprintf("  %-12s %-8s %s", "SERVICE", "PID", "STATE")) + "\n")
	for i, s := range m.rows {
		cursor := "  "
		line := fmt.Sprintf("%-12s %-8s %s", s.Name, pidText(s.PID), stateStyle(s.State).Render(s.State))
		if i == m.cursor && m.focus == focusTable {
			cursor = cursorStyle.Render("▌ ")
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		b.WriteString(cursor + line + "\n")
	}

	logTitle := scopeStyle.Render("logs")
	var hints []string
	if m.filterSvc != "" {
		hints = append(hints, "filter: "+m.filterSvc)
	}
	if m.xOffset > 0 {
		hints = append(hints, fmt.Sprintf("col +%d", m.xOffset))
	}
	if m.focus == focusLogs {
		hints = append(hints, "scrolling — tab to return")
	}
	if len(hints) > 0 {
		logTitle += faintStyle.Render(" (" + strings.Join(hints, "; ") + ")")
	}
	b.WriteString("\n" + logTitle + "\n")
	b.WriteString(m.vp.View() + "\n")

	status := faintStyle.Render(m.status)
	if m.busy {
		status = warnStyle.Render(m.status)
	}
	b.WriteString(status + "  " + faintStyle.Render("[↑/↓] select  [s]tart [x]stop [r]estart  [f]ilter  [tab] logs  [←/→] scroll  [q]uit"))
	return b.String()
}

func pidText(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", pid)
}

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "healthy":
		return healthyState
	case "starting":
		return startingState
	default:
		return stoppedState
	}
}
