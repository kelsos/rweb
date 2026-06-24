package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kelsos/rweb/internal/proc"
)

func sampleLines() []proc.LogLine {
	return []proc.LogLine{
		{Service: "django", Text: "django up"},
		{Service: "nuxt", Text: "nuxt up"},
		{Service: "django", Text: "django req"},
	}
}

func TestRenderLogLinesFilter(t *testing.T) {
	tags := map[string]lipgloss.Style{"django": {}, "nuxt": {}}

	// No filter: every line is rendered.
	all := renderLogLines(sampleLines(), "", tags, 0, 80)
	if n := strings.Count(all, "\n") + 1; n != 3 {
		t.Fatalf("expected 3 lines unfiltered, got %d:\n%s", n, all)
	}

	// Filter to django: only its lines remain.
	only := renderLogLines(sampleLines(), "django", tags, 0, 80)
	lines := strings.Split(only, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 django lines, got %d:\n%s", len(lines), only)
	}
	if strings.Contains(only, "nuxt up") {
		t.Error("filtered view should not contain nuxt lines")
	}
}

// A horizontal offset drops leading columns; ANSI-aware Cut keeps the result a
// valid (shorter) string. Offset 0 leaves content untouched.
func TestRenderLogLinesHorizontalOffset(t *testing.T) {
	tags := map[string]lipgloss.Style{"django": lipgloss.NewStyle().Bold(true)}
	raw := []proc.LogLine{{Service: "django", Text: "0123456789abcdef"}}

	full := renderLogLines(raw, "", tags, 0, 80)
	shifted := renderLogLines(raw, "", tags, 10, 80)

	if ansi.StringWidth(shifted) >= ansi.StringWidth(full) {
		t.Errorf("offset should shorten the visible line: full=%d shifted=%d",
			ansi.StringWidth(full), ansi.StringWidth(shifted))
	}
	// The tail of the content survives; the head is scrolled off.
	if !strings.Contains(ansi.Strip(shifted), "abcdef") {
		t.Errorf("scrolled view should still show the tail: %q", ansi.Strip(shifted))
	}
	if strings.Contains(ansi.Strip(shifted), "django") {
		t.Errorf("scrolled-past service tag should be gone: %q", ansi.Strip(shifted))
	}
}
