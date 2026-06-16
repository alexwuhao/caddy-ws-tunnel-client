package agent

import (
	"fmt"
	"sync"
)

// Allowlist maps numeric port id (as string key) to local "host:port".
// All methods are safe for concurrent use and support atomic hot-replace.
type Allowlist struct {
	mu    sync.RWMutex
	ports map[string]string // port_id -> "host:port"
}

// Replace atomically swaps the full allowlist.
func (a *Allowlist) Replace(ports map[string]string) {
	cp := make(map[string]string, len(ports))
	for k, v := range ports {
		cp[k] = v
	}
	a.mu.Lock()
	a.ports = cp
	a.mu.Unlock()
}

// Lookup returns the local address for portID and whether it is allowed.
func (a *Allowlist) Lookup(portID uint64) (addr string, ok bool) {
	key := fmt.Sprintf("%d", portID)
	a.mu.RLock()
	addr, ok = a.ports[key]
	a.mu.RUnlock()
	return
}
