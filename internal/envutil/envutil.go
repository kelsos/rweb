// Package envutil loads repo .env-style files and composes child-process
// environments (base os.Environ + parsed files + secret overlays).
package envutil

import (
	"os"
	"strings"
)

// ParseFile reads a KEY=VALUE / `export KEY=VALUE` file (shell-ish, as used by
// rotkehlchen-web's localtest_env). Comments and blank lines are ignored and
// surrounding quotes are stripped. A missing file is not an error to callers
// that ignore it; here it returns the read error.
func ParseFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(s[:eq])
		val := strings.TrimSpace(s[eq+1:])
		val = strings.Trim(val, `"'`)
		m[key] = val
	}
	return m, nil
}

// Compose returns an environment slice (os.Environ plus the merged contents of
// the given files, then the overlays applied last so they win). Missing files
// are skipped silently.
func Compose(files []string, overlays ...map[string]string) []string {
	return ComposeWithBase(nil, files, overlays...)
}

// ComposeWithBase is Compose with an additional base layer merged first, beneath
// the files — the lowest-precedence managed layer. The resulting precedence is:
// os.Environ < base < files < overlays. Used to inject a non-secret env baseline
// that repo .env files (when present) may override.
func ComposeWithBase(base map[string]string, files []string, overlays ...map[string]string) []string {
	merged := map[string]string{}
	for k, v := range base {
		merged[k] = v
	}
	for _, f := range files {
		if m, err := ParseFile(f); err == nil {
			for k, v := range m {
				merged[k] = v
			}
		}
	}
	for _, o := range overlays {
		for k, v := range o {
			merged[k] = v
		}
	}
	env := os.Environ()
	for k, v := range merged {
		env = append(env, k+"="+v)
	}
	return env
}
