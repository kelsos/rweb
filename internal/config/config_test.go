package config

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultProfilesAndPorts(t *testing.T) {
	cfg := Default()
	full, ok := cfg.Profiles["full"]
	if !ok {
		t.Fatal("missing full profile")
	}
	want := []string{"docker", "django", "huey", "go-dev", "nuxt"}
	if len(full.Services) != len(want) {
		t.Fatalf("full services = %v", full.Services)
	}
	for i := range want {
		if full.Services[i] != want[i] {
			t.Fatalf("full services = %v, want %v", full.Services, want)
		}
	}
	if cfg.Ports.Django != 8000 || cfg.Ports.Postgres != 5432 {
		t.Errorf("unexpected default ports: %+v", cfg.Ports)
	}
	if cfg.Tools.UV != "uv" || cfg.Tools.PNPM != "pnpm" {
		t.Errorf("unexpected default tools: %+v", cfg.Tools)
	}
}

// A partial config.toml should unmarshal onto the defaults, overriding only the
// fields it sets and leaving everything else at its default — the contract Load
// relies on.
func TestPartialTOMLOverlaysDefaults(t *testing.T) {
	partial := `
[repos]
rotkehlchen_web = "/repos/web"

[ports]
django = 9000
`
	cfg := Default()
	if err := toml.Unmarshal([]byte(partial), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Repos.RotkehlchenWeb != "/repos/web" {
		t.Errorf("repo not applied: %q", cfg.Repos.RotkehlchenWeb)
	}
	if cfg.Ports.Django != 9000 {
		t.Errorf("port override not applied: %d", cfg.Ports.Django)
	}
	// Untouched fields keep their defaults.
	if cfg.Ports.Postgres != 5432 {
		t.Errorf("default port clobbered: %d", cfg.Ports.Postgres)
	}
	if _, ok := cfg.Profiles["review-static"]; !ok {
		t.Error("default profiles should survive a partial overlay")
	}
}

func TestConfigTOMLRoundTrips(t *testing.T) {
	cfg := Default()
	cfg.Repos.RotkiCom = "/repos/com"

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		t.Fatal(err)
	}
	decoded := &Config{}
	if err := toml.Unmarshal(buf.Bytes(), decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Repos.RotkiCom != "/repos/com" {
		t.Errorf("round-trip lost repo: %q", decoded.Repos.RotkiCom)
	}
	if decoded.DB.Name != "rotkehlchen_db" {
		t.Errorf("round-trip lost db name: %q", decoded.DB.Name)
	}
}

func TestSecretsPathFor(t *testing.T) {
	base := SecretsPath()
	// The base/default env (empty or "default") uses secrets.age.
	if got := SecretsPathFor(""); got != base {
		t.Errorf("empty env should map to base secrets path %q, got %q", base, got)
	}
	if got := SecretsPathFor(DefaultEnv); got != base {
		t.Errorf("default env should map to base secrets path %q, got %q", base, got)
	}
	// A named env uses a sibling secrets.<name>.age beside the base file.
	want := filepath.Join(Dir(), "secrets.staging.age")
	if got := SecretsPathFor("staging"); got != want {
		t.Errorf("named env path = %q, want %q", got, want)
	}
}
