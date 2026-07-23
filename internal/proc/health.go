package proc

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// checkHealth returns nil when the service satisfies its readiness probe. A
// service with no TCP/HTTP/Postgres probe is considered healthy as soon as it
// is alive.
func checkHealth(h Health) error {
	for _, addr := range h.TCP {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return fmt.Errorf("tcp %s: %w", addr, err)
		}
		conn.Close()
	}
	if h.HTTP != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(h.HTTP)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("http %s: status %d", h.HTTP, resp.StatusCode)
		}
	}
	if h.Postgres != nil {
		if err := h.Postgres.check(); err != nil {
			return err
		}
	}
	return nil
}

// check runs pg_isready inside the postgres container. Unlike a bare TCP dial
// to the mapped port, this confirms the server has finished initdb and is
// accepting connections, which is what migrations and app servers actually
// need. A non-existent container (compose exec failing) is just an unready
// probe, so the caller keeps polling.
func (p *PGProbe) check() error {
	c := exec.Command(p.Docker, "compose", "exec", "-T", "postgres",
		"pg_isready", "-U", "postgres", "-q")
	c.Dir = p.Dir
	if out, err := c.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("pg_isready: %w: %s", err, msg)
		}
		return fmt.Errorf("pg_isready: %w", err)
	}
	return nil
}

// WaitTCP blocks until a host:port accepts a connection or the timeout hits.
func WaitTCP(addr string, timeout time.Duration) error {
	return waitHealthy(addr, Health{TCP: []string{addr}}, timeout, nil)
}

// waitHealthy polls until the probe passes, the process dies, or the timeout hits.
func waitHealthy(name string, h Health, timeout time.Duration, alive func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if alive != nil && !alive() {
			return fmt.Errorf("%s exited before becoming healthy (check logs)", name)
		}
		if err := checkHealth(h); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not healthy after %s", name, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
