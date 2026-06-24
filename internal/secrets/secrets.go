// Package secrets manages an age-encrypted secret store. The age identity
// (private key) is kept in the OS keychain so unlock is automatic; secrets are
// only ever decrypted into memory and never written to disk in plaintext.
package secrets

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"filippo.io/age"
	"github.com/BurntSushi/toml"
	"github.com/zalando/go-keyring"
)

// Secret scopes. Values are grouped so each child process only receives the
// variables it needs (shared is merged into every scope).
const (
	ScopeShared    = "shared"
	ScopeDjango    = "django"
	ScopeGoBackend = "go-backend"
	ScopeNuxt      = "nuxt"
)

// envKeyOverride lets headless/CI runs supply the identity without a keychain.
const envKeyOverride = "RWEB_AGE_KEY"

// Store is a handle to the encrypted secret file plus key custody settings.
type Store struct {
	path      string
	krService string
	krUser    string
	keyFile   string
	recipient string // public key from config; empty means derive from identity
}

// New constructs a Store.
func New(path, krService, krUser, keyFile, recipient string) *Store {
	return &Store{path: path, krService: krService, krUser: krUser, keyFile: keyFile, recipient: recipient}
}

// Init generates a fresh age identity, stores the private key in the OS keychain
// (falling back to a 0600 key file when no keychain is available), seeds an empty
// encrypted store, and returns the recipient (public key) plus the key mode used.
func (s *Store) Init() (recipient, mode string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	mode = "keychain"
	if kerr := keyring.Set(s.krService, s.krUser, id.String()); kerr != nil {
		if werr := os.WriteFile(s.keyFile, []byte(id.String()+"\n"), 0o600); werr != nil {
			return "", "", fmt.Errorf("keychain unavailable (%v) and key file write failed: %w", kerr, werr)
		}
		mode = "file"
	}
	s.recipient = id.Recipient().String()
	if err := s.write(map[string]map[string]string{}); err != nil {
		return "", "", err
	}
	return s.recipient, mode, nil
}

// identity resolves the age private key: env override, then keychain, then key file.
func (s *Store) identity() (*age.X25519Identity, error) {
	if k := os.Getenv(envKeyOverride); k != "" {
		return age.ParseX25519Identity(strings.TrimSpace(k))
	}
	if k, err := keyring.Get(s.krService, s.krUser); err == nil && k != "" {
		return age.ParseX25519Identity(strings.TrimSpace(k))
	}
	if b, err := os.ReadFile(s.keyFile); err == nil {
		return age.ParseX25519Identity(strings.TrimSpace(string(b)))
	}
	return nil, fmt.Errorf("no age identity found (keychain / %s / %s); run `rweb init`", s.keyFile, envKeyOverride)
}

func (s *Store) recipientObj() (age.Recipient, error) {
	if s.recipient != "" {
		return age.ParseX25519Recipient(s.recipient)
	}
	id, err := s.identity()
	if err != nil {
		return nil, err
	}
	return id.Recipient(), nil
}

// Read decrypts the store into a scope -> key -> value map.
func (s *Store) Read() (map[string]map[string]string, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("open secret store (run `rweb init`?): %w", err)
	}
	defer f.Close()
	id, err := s.identity()
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(f, id)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret store: %w", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := toml.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("parse decrypted secrets: %w", err)
		}
	}
	return out, nil
}

func (s *Store) write(m map[string]map[string]string) error {
	rec, err := s.recipientObj()
	if err != nil {
		return err
	}
	var plain bytes.Buffer
	if err := toml.NewEncoder(&plain).Encode(m); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w, err := age.Encrypt(out, rec)
	if err != nil {
		out.Close()
		return err
	}
	if _, err := w.Write(plain.Bytes()); err != nil {
		w.Close()
		out.Close()
		return err
	}
	if err := w.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Set stores a single secret value.
func (s *Store) Set(scope, key, val string) error {
	m, err := s.Read()
	if err != nil {
		return err
	}
	if m[scope] == nil {
		m[scope] = map[string]string{}
	}
	m[scope][key] = val
	return s.write(m)
}

// Rm deletes a single secret value.
func (s *Store) Rm(scope, key string) error {
	m, err := s.Read()
	if err != nil {
		return err
	}
	if m[scope] != nil {
		delete(m[scope], key)
	}
	return s.write(m)
}

// Keys returns the secret key names per scope (never values), for redacted listing.
func (s *Store) Keys() (map[string][]string, error) {
	m, err := s.Read()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for scope, kv := range m {
		ks := make([]string, 0, len(kv))
		for k := range kv {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[scope] = ks
	}
	return out, nil
}

// EnvFor returns the merged shared+scope environment map for injecting into a child.
func (s *Store) EnvFor(scope string) (map[string]string, error) {
	m, err := s.Read()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range m[ScopeShared] {
		out[k] = v
	}
	for k, v := range m[scope] {
		out[k] = v
	}
	return out, nil
}

// ReadTOML returns the decrypted store as TOML bytes (for `secret edit`).
func (s *Store) ReadTOML() ([]byte, error) {
	m, err := s.Read()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteTOML re-encrypts a TOML document into the store (for `secret edit`).
func (s *Store) WriteTOML(b []byte) error {
	m := map[string]map[string]string{}
	if err := toml.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("invalid TOML, not saving: %w", err)
	}
	return s.write(m)
}
