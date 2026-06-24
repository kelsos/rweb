package proc

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// checkHealth returns nil when the service satisfies its readiness probe. A
// service with no TCP/HTTP probe is considered healthy as soon as it is alive.
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
