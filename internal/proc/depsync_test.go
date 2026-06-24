package proc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDepDrift(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "uv.lock")

	// Missing lockfile is skipped, never synced.
	if _, need := depDrift(lock, map[string]int64{}, false); need {
		t.Fatal("missing lockfile should not need a sync")
	}

	if err := os.WriteFile(lock, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First sight of a lockfile needs a sync.
	mod, need := depDrift(lock, map[string]int64{}, false)
	if !need {
		t.Fatal("unseen lockfile should need a sync")
	}

	// Recorded at its current mtime → no drift.
	stamps := map[string]int64{lock: mod}
	if _, need := depDrift(lock, stamps, false); need {
		t.Fatal("unchanged lockfile should not need a sync")
	}

	// force overrides an up-to-date stamp.
	if _, need := depDrift(lock, stamps, true); !need {
		t.Fatal("force should always need a sync")
	}

	// A newer mtime is drift.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(lock, future, future); err != nil {
		t.Fatal(err)
	}
	if _, need := depDrift(lock, stamps, false); !need {
		t.Fatal("changed lockfile should need a sync")
	}
}
