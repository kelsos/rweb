package cli

import (
	"testing"

	"github.com/kelsos/rweb/internal/secrets"
)

func TestLooksLikeSecret(t *testing.T) {
	// looksLikeSecret is a NAME heuristic, not manifest membership: it matches the
	// PASS/SECRET/TOKEN/… tokens. (HASHID_FIELD_SALT is a manifest secret but its
	// name has no such token, so it isn't matched — a known heuristic limitation.)
	secret := []string{
		"POSTGRES_PASSWORD", "DB_PASS", "DJANGO_SECRET_KEY",
		"GOOGLE_CLIENT_SECRET", "SOME_TOKEN", "API_CREDENTIAL",
	}
	for _, k := range secret {
		if !looksLikeSecret(k) {
			t.Errorf("%q should be classified as a secret", k)
		}
	}
	nonSecret := []string{
		"DOMAIN", "DB_HOST", "NUXT_PUBLIC_GOOGLE_CLIENT_ID",
		"NUXT_PUBLIC_RECAPTCHA_SITE_KEY", "NUXT_PUBLIC_WALLET_CONNECT_PROJECT_ID",
		// public by definition even if the name contains a secret-ish token
		"NUXT_PUBLIC_SOME_TOKEN", "NUXT_PUBLIC_API_KEY",
	}
	for _, k := range nonSecret {
		if looksLikeSecret(k) {
			t.Errorf("%q should NOT be classified as a secret", k)
		}
	}
}

// NUXT_PUBLIC_* values are public (baked into the client bundle), so they must
// not appear in the secret manifest — only in the non-secret env catalog.
func TestNuxtPublicKeysNotInSecretManifest(t *testing.T) {
	for _, k := range secrets.Manifest[secrets.ScopeNuxt] {
		t.Errorf("nuxt secret manifest should be empty (public values aren't secrets), found %q", k)
	}
	if len(optionalEnv[secrets.ScopeNuxt]) == 0 {
		t.Error("nuxt public keys should be documented in the optionalEnv catalog")
	}
}
