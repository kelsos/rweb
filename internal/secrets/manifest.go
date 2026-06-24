package secrets

// Manifest lists the secret keys rweb expects per scope. `rweb env doctor`
// diffs this against the store and reports missing names (never values).
// Derived from rotkehlchen-web (localtest_env, .env) and rotki.com (backend/.env,
// packages/website/.env). Non-secret connection details (DB_HOST/PORT/NAME/USER,
// REDIS_HOST) are not listed here: rweb derives them from config and injects
// them (see proc.derivedEnv), so only the passwords/keys remain secrets.
var Manifest = map[string][]string{
	ScopeShared: {
		"POSTGRES_PASSWORD",
		"REDIS_PASSWORD",
	},
	ScopeDjango: {
		"DJANGO_SECRET_KEY",
		"HASHID_FIELD_SALT",
		"DB_PASS",
		"BRAINTREE_MERCHANT_ID",
		"BRAINTREE_PUBLIC_KEY",
		"BRAINTREE_PRIVATE_KEY",
		"RECAPTCHA_PUBLIC_KEY",
		"RECAPTCHA_PRIVATE_KEY",
	},
	ScopeGoBackend: {
		"GOOGLE_CLIENT_SECRET",
		"MONERIUM_CLIENT_SECRET",
	},
	// nuxt has no secrets: every NUXT_PUBLIC_* value is baked into the client
	// bundle and is therefore public by definition. They live in the non-secret
	// optionalEnv catalog (cli/catalog.go) and the [env.nuxt] config, not here.
	ScopeNuxt: {},
}

// Scopes returns the canonical scope order for display.
func Scopes() []string {
	return []string{ScopeShared, ScopeDjango, ScopeGoBackend, ScopeNuxt}
}
