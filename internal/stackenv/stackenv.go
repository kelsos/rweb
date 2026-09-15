// Package stackenv resolves the non-secret env layers and secret source rweb
// applies to a scope. Both the supervised services (proc) and one-shot Django
// management commands (db) compose their environment from it, so `rweb db
// migrate` sees the same config as the running stack.
package stackenv

import (
	"fmt"

	"github.com/kelsos/rweb/internal/config"
	"github.com/kelsos/rweb/internal/secrets"
)

// Google's universal reCAPTCHA v2 test keys — public, always-pass values meant
// for development. Shipped as dev defaults; DJANGO_DEBUG=True silences the
// test-key warning. A real key set in the secret store overrides them.
const (
	RecaptchaTestSiteKey   = "6LeIxAcTAAAAAJcZVRqyHh71UMIEGNQ_MXjiZKhI"
	RecaptchaTestSecretKey = "6LeIxAcTAAAAAGG-vFI1TnRWxMZNFuojJ4WifJWe"
)

// Derived returns non-secret env values rweb computes from config for a
// scope, so they need not be set by hand or duplicated in secrets. These sit at
// the managed-baseline level, so repo .env files and explicit [env.*] entries
// still override them. Several Django settings hard-fail (ValueError) when
// unset, so we supply dev-safe defaults here to keep the "a fresh checkout with
// no .env files still boots" promise.
func Derived(cfg *config.Config, scope string) map[string]string {
	switch scope {
	case secrets.ScopeShared:
		return map[string]string{"REDIS_HOST": "localhost"}
	case secrets.ScopeDjango:
		return map[string]string{
			"DB_HOST": cfg.DB.Host,
			"DB_PORT": fmt.Sprintf("%d", cfg.DB.Port),
			"DB_NAME": cfg.DB.Name,
			"DB_USER": cfg.DB.User,
			"DB_TYPE": "postgres",
			// Required-but-defaultable settings Django raises ValueError without.
			"DJANGO_DEBUG":            "True",
			"DOMAIN":                  "localhost",
			"UPLOADED_BACKUPS_FOLDER": "data/backups",
			"BRAINTREE_PRODUCTION":    "False",
			// Email → mailpit (SMTP :1025), viewable via `rweb open mailpit`.
			"EMAIL_TYPE": "MAILHOG",
			"EMAIL_HOST": "localhost",
			"EMAIL_PORT": "1025",
			// reCAPTCHA dev test keys (overridden by a real secret if set).
			"RECAPTCHA_PUBLIC_KEY":  RecaptchaTestSiteKey,
			"RECAPTCHA_PRIVATE_KEY": RecaptchaTestSecretKey,
		}
	case secrets.ScopeNuxt:
		return map[string]string{
			"NUXT_PUBLIC_RECAPTCHA_SITE_KEY": RecaptchaTestSiteKey,
		}
	}
	return map[string]string{}
}

// Baseline returns the managed non-secret baseline for a scope: the derived
// defaults with the user's [env.<scope>] entries on top. It sits beneath repo
// .env files.
func Baseline(cfg *config.Config, scope string) map[string]string {
	base := Derived(cfg, scope)
	for k, v := range cfg.Env[scope] {
		base[k] = v
	}
	return base
}

// Overlay returns the active named environment's non-secret overlay for a
// scope (nil for the default/base env). It sits above repo .env files.
func Overlay(cfg *config.Config, scope string) map[string]string {
	if cfg.ActiveEnv == "" || cfg.ActiveEnv == config.DefaultEnv {
		return nil
	}
	p, ok := cfg.Environments[cfg.ActiveEnv]
	if !ok {
		return nil
	}
	return p.Env[scope]
}

// SecretSource provides a scope's secret env. Both *secrets.Store and the
// base+overlay merge below satisfy it.
type SecretSource interface {
	EnvFor(scope string) (map[string]string, error)
}

// multiSource merges a base secret store with a named environment's overlay
// store; the overlay wins on conflicts.
type multiSource struct{ base, env SecretSource }

func (m multiSource) EnvFor(scope string) (map[string]string, error) {
	out := map[string]string{}
	b, err := m.base.EnvFor(scope)
	if err != nil {
		return nil, err
	}
	for k, v := range b {
		out[k] = v
	}
	e, err := m.env.EnvFor(scope)
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		out[k] = v
	}
	return out, nil
}

// Source resolves the secret source for the active environment: the single
// store for the default env, or the base store overlaid by the active env's
// store for a named environment (env wins). active is the store the caller
// already built for the active env (secrets.<active>.age).
func Source(cfg *config.Config, active *secrets.Store) SecretSource {
	if cfg.ActiveEnv == "" || cfg.ActiveEnv == config.DefaultEnv {
		return active
	}
	base := secrets.New(
		config.SecretsPathFor(config.DefaultEnv),
		cfg.Secrets.KeyringService,
		cfg.Secrets.KeyringUser,
		config.KeyFile(),
		cfg.Secrets.AgeRecipient,
	)
	return multiSource{base: base, env: active}
}
