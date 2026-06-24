package cli

import "github.com/kelsos/rweb/internal/secrets"

// optionalSecrets are secret-manifest keys that are NOT boot-blocking: either
// feature-gated integrations (OAuth, WalletConnect) that default empty in the
// apps, or values rweb ships a safe dev default for (reCAPTCHA test keys, the
// Braintree sandbox flags). `env doctor` reports these informationally instead
// of failing on them. The truly-required secrets (everything in the manifest
// not listed here) still fail doctor when absent.
var optionalSecrets = map[string]bool{
	// django — dev defaults shipped via derivedEnv, or feature-gated
	"BRAINTREE_MERCHANT_ID": true,
	"BRAINTREE_PUBLIC_KEY":  true,
	"BRAINTREE_PRIVATE_KEY": true,
	"RECAPTCHA_PUBLIC_KEY":  true,
	"RECAPTCHA_PRIVATE_KEY": true,
	// go-backend — empty default, only needed for the OAuth flows
	"GOOGLE_CLIENT_SECRET":   true,
	"MONERIUM_CLIENT_SECRET": true,
	// nuxt — every NUXT_PUBLIC_* value is baked into the client bundle, so it is
	// public, not secret. Those live in the optionalEnv catalog below, not in the
	// secret manifest.
}

// catalogEntry documents an optional non-secret env var the stack understands,
// with its default and a one-line description. Surfaced by `rweb env manifest`
// and overridable per environment with `rweb env set`.
type catalogEntry struct {
	Key  string
	Def  string
	Desc string
}

// optionalEnv is the catalog of optional non-secret feature-flags / tunables
// discovered from the apps. Defaults are the apps' own defaults unless noted as
// a dev-env override.
var optionalEnv = map[string][]catalogEntry{
	secrets.ScopeGoBackend: {
		{"SPONSORSHIP_ENABLED", "false", "enable sponsor/mint flow (dev env defaults true)"},
		{"TESTING", "false", "test posture; relayed to client via /api/config → testnet chains (dev env defaults true)"},
		{"MAINTENANCE", "false", "maintenance page"},
		{"LOG_LEVEL", "info", "debug|info|warn|error"},
		{"IMAGE_CACHE_DIR", "./image-cache", "image cache directory"},
		{"MONERIUM_AUTH_BASE_URL", "https://api.monerium.dev", "Monerium auth host"},
	},
	secrets.ScopeDjango: {
		{"USE_CRYPTO_TESTNET", "(unset)", "presence flag: BTC testnet (dev env sets it; same axis as go TESTING)"},
		{"DJANGO_LOG_LEVEL", "INFO", "Django logger level"},
		{"LOG_LEVEL", "DEBUG", "app logger level"},
		{"ROTKI_RUN_REFERRAL_BRAINTREE_TASK", "True", "run referral credit task"},
		{"NFT_CONTRACT_ADDRESS", "0x9C4Ac51128b3B29c8c4C76c960a07c17b8290557", "NFT contract (sepolia)"},
		{"ETHEREUM_CHAIN_NAME", "sepolia", "chain name"},
		{"ALLOWED_HOSTS", "*", "Django allowed hosts"},
	},
	secrets.ScopeNuxt: {
		{"NUXT_PUBLIC_RECAPTCHA_SITE_KEY", "(test key)", "public reCAPTCHA site key (rweb ships Google's test key)"},
		{"NUXT_PUBLIC_WALLET_CONNECT_PROJECT_ID", "", "public WalletConnect project id (feature-gated)"},
		{"NUXT_PUBLIC_GOOGLE_CLIENT_ID", "", "public Google OAuth client id (feature-gated)"},
		{"NUXT_PUBLIC_MONERIUM_AUTHORIZATION_CODE_FLOW_CLIENT_ID", "", "public Monerium client id (feature-gated)"},
		{"NUXT_PUBLIC_MAINTENANCE", "false", "client maintenance flag (client reads /api/config; likely unused)"},
		{"NUXT_PUBLIC_TESTING", "true", "client testing flag (client reads /api/config; likely unused)"},
		{"NUXT_PUBLIC_MONERIUM_AUTH_BASE_URL", "https://api.monerium.dev", "Monerium auth host"},
	},
}

// postureEnv is the "dev testnet posture" — the small set of non-secret values
// that differ from the apps' own production defaults to put a local stack on
// testnet. go-backend TESTING is the master switch (relayed to the client via
// /api/config → testnet chains); django USE_CRYPTO_TESTNET is the backend-side
// twin (BTC testnet). These are seeded into a new environment's [env.*] overlay
// (above repo .env) so selecting the env authoritatively forces the posture.
// All non-secret, so seeding carries no secret-store risk.
var postureEnv = map[string]map[string]string{
	secrets.ScopeGoBackend: {
		"TESTING":             "true",
		"SPONSORSHIP_ENABLED": "true",
	},
	secrets.ScopeDjango: {
		"USE_CRYPTO_TESTNET":   "1",
		"BRAINTREE_PRODUCTION": "False",
	},
}

// seedPostureEnv writes the dev testnet posture into an [env.*] overlay map,
// creating scope maps as needed, and returns how many values it set.
func seedPostureEnv(env map[string]map[string]string) int {
	n := 0
	for scope, kv := range postureEnv {
		if env[scope] == nil {
			env[scope] = map[string]string{}
		}
		for k, v := range kv {
			env[scope][k] = v
			n++
		}
	}
	return n
}

// requiredManifest returns the boot-blocking required secret keys for a scope
// (the manifest minus the optional set).
func requiredManifest(scope string) []string {
	var req []string
	for _, k := range secrets.Manifest[scope] {
		if !optionalSecrets[k] {
			req = append(req, k)
		}
	}
	return req
}

// optionalManifest returns the optional (non-blocking) secret keys for a scope.
func optionalManifest(scope string) []string {
	var opt []string
	for _, k := range secrets.Manifest[scope] {
		if optionalSecrets[k] {
			opt = append(opt, k)
		}
	}
	return opt
}
