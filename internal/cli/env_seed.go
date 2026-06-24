package cli

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
)

// secretSeed is one required secret to write into an environment's store.
type secretSeed struct {
	scope, key, val string
}

// randToken returns a URL-safe random string with at least nBytes of entropy.
func randToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// placeholderSeeds are the project's well-known dev placeholders (from
// rotkehlchen-web's localtest_env). POSTGRES_PASSWORD seeds the docker postgres
// and DB_PASS is what Django connects with, so they MUST match — both "123".
func placeholderSeeds() []secretSeed {
	return []secretSeed{
		{secrets.ScopeShared, "POSTGRES_PASSWORD", "123"},
		{secrets.ScopeShared, "REDIS_PASSWORD", "1234"},
		{secrets.ScopeDjango, "DJANGO_SECRET_KEY", "dev-insecure-secret-key"},
		{secrets.ScopeDjango, "HASHID_FIELD_SALT", "supersecret"},
		{secrets.ScopeDjango, "DB_PASS", "123"},
	}
}

// generatedSeeds are throwaway random secrets for the same required keys. The
// postgres password and Django's DB_PASS share one generated value so the
// database actually connects.
func generatedSeeds() ([]secretSeed, error) {
	dbPass, err := randToken(18)
	if err != nil {
		return nil, err
	}
	redis, err := randToken(18)
	if err != nil {
		return nil, err
	}
	djangoKey, err := randToken(48)
	if err != nil {
		return nil, err
	}
	salt, err := randToken(24)
	if err != nil {
		return nil, err
	}
	return []secretSeed{
		{secrets.ScopeShared, "POSTGRES_PASSWORD", dbPass},
		{secrets.ScopeShared, "REDIS_PASSWORD", redis},
		{secrets.ScopeDjango, "DJANGO_SECRET_KEY", djangoKey},
		{secrets.ScopeDjango, "HASHID_FIELD_SALT", salt},
		{secrets.ScopeDjango, "DB_PASS", dbPass},
	}, nil
}

// seedsForMode returns the seeds for a --seed-secrets mode ("placeholders" or
// "generate"); "none"/"" yields nil.
func seedsForMode(mode string) ([]secretSeed, error) {
	switch mode {
	case "", "none":
		return nil, nil
	case "placeholders":
		return placeholderSeeds(), nil
	case "generate":
		return generatedSeeds()
	default:
		return nil, fmt.Errorf("unknown --seed-secrets mode %q (want: none|placeholders|generate)", mode)
	}
}

// ensureEnvSecretStore returns a Store for the named environment's secret file,
// creating it (empty, encrypted to the existing identity) when it does not yet
// exist. Creating it via WriteTOML reuses the keychain identity rather than
// re-running Init, which would mint a fresh identity and orphan the base store.
// Returns created=true when a new file was written. An error here means no
// identity is available yet (run `rweb init`).
func ensureEnvSecretStore(cfg *config.Config, name string) (st *secrets.Store, created bool, err error) {
	envCfg := *cfg
	envCfg.ActiveEnv = name
	store := storeFromCfg(&envCfg)
	if fileExists(config.SecretsPathFor(name)) {
		return store, false, nil
	}
	if err := store.WriteTOML([]byte{}); err != nil {
		return nil, false, err
	}
	return store, true, nil
}

// seedSecrets writes the given seeds into a store, skipping keys that already
// exist, and returns how many it set. Used at environment creation to populate
// the required secrets so the stack boots without hand-entering each one.
func seedSecrets(st *secrets.Store, seeds []secretSeed) (int, error) {
	have, err := st.Keys()
	if err != nil {
		return 0, err
	}
	present := map[string]map[string]bool{}
	for scope, keys := range have {
		present[scope] = map[string]bool{}
		for _, k := range keys {
			present[scope][k] = true
		}
	}
	n := 0
	for _, s := range seeds {
		if present[s.scope][s.key] {
			continue
		}
		if err := st.Set(s.scope, s.key, s.val); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
