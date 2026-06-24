package proc

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kelsos/rweb/internal/config"
)

// LogLine is one line read from a service's captured log, tagged with its source.
type LogLine struct {
	Service string
	Text    string
}

// MultiTail streams interleaved log lines from the named services' log files. It
// returns a receive channel of LogLines and a stop func. When follow is false it
// drains each file's existing content (grouped per service) and closes the
// channel; when follow is true it keeps polling for appended lines until stop()
// is called. The channel is closed once all readers finish.
func MultiTail(names []string, follow bool) (<-chan LogLine, func()) {
	ch := make(chan LogLine, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			tailFile(filepath.Join(logDir(), name+".log"), name, follow, ch, done)
		}(name)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch, stop
}

// tailFile emits complete lines from path, holding back partial (un-terminated)
// lines while following so a line is never split across two LogLines.
func tailFile(path, name string, follow bool, ch chan<- LogLine, done <-chan struct{}) {
	f := openWithWait(path, follow, done)
	if f == nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	var pending string
	emit := func(text string) bool {
		select {
		case ch <- LogLine{Service: name, Text: text}:
			return true
		case <-done:
			return false
		}
	}
	for {
		line, err := reader.ReadString('\n')
		if err == nil {
			if !emit(pending + strings.TrimRight(line, "\r\n")) {
				return
			}
			pending = ""
			continue
		}
		pending += line
		if err != io.EOF {
			return
		}
		if !follow {
			if pending != "" {
				emit(strings.TrimRight(pending, "\r\n"))
			}
			return
		}
		select {
		case <-done:
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// openWithWait opens path; when following, it waits for the file to appear
// rather than giving up, returning nil only if asked to stop.
func openWithWait(path string, follow bool, done <-chan struct{}) *os.File {
	f, err := os.Open(path)
	if err == nil {
		return f
	}
	if !follow {
		return nil
	}
	for {
		select {
		case <-done:
			return nil
		case <-time.After(400 * time.Millisecond):
		}
		if f, err = os.Open(path); err == nil {
			return f
		}
	}
}

// TrackedNames returns the currently tracked long-running service names, ordered
// by the active profile where possible (leftovers appended in map order).
func TrackedNames(cfg *config.Config) []string {
	state := LoadState()
	var names []string
	seen := map[string]bool{}
	for _, name := range serviceOrder(cfg, state.Profile) {
		if _, ok := state.Instances[name]; ok {
			names = append(names, name)
			seen[name] = true
		}
	}
	for name := range state.Instances {
		if !seen[name] {
			names = append(names, name)
		}
	}
	return names
}
