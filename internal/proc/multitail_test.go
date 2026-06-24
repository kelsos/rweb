package proc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// drain reads tailFile's output into a slice of "service: text" strings.
func collect(path, name string, follow bool, done <-chan struct{}) []string {
	ch := make(chan LogLine, 64)
	go func() {
		tailFile(path, name, follow, ch, done)
		close(ch)
	}()
	var out []string
	for l := range ch {
		out = append(out, l.Service+": "+l.Text)
	}
	return out
}

func TestTailFileOneShotEmitsAllLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc.log")
	// Last line has no trailing newline; it must still be emitted.
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(path, "svc", false, nil)
	want := []string{"svc: alpha", "svc: beta", "svc: gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTailFileFollowReadsAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ch := make(chan LogLine, 64)
	go tailFile(path, "svc", true, ch, done)

	expect := func(want string) {
		t.Helper()
		select {
		case l := <-ch:
			if l.Text != want {
				t.Fatalf("got %q, want %q", l.Text, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	expect("first")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A partial write must not be emitted until its newline arrives.
	if _, err := f.WriteString("sec"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("ond\nthird\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	expect("second")
	expect("third")
	close(done)
}
