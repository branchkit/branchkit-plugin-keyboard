package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

// Host is what every handler needs: the platform handle, the plugin state
// with the mutex that guards it, and the hold-to-repeat machinery. Handlers
// are methods on it, so a handler's dependencies are visible in its
// signature and the mutex sits beside the data it protects.
type Host struct {
	plugin *branchkit.Plugin

	mu    sync.Mutex
	state *PluginState

	// Hold-to-repeat: the one active hold, its sequence counter, the system
	// repeat timings, and modifiers held via hold mode (injected into key
	// actions).
	activeHold    *holdState
	holdSeq       atomic.Uint64
	repeatCfg     repeatConfig
	heldModifiers []string
	// rawKey sends one raw key event (press, release, click) and
	// safetyTimeout bounds how long a repeating hold may run without its
	// stop. Seams so the hold machinery can be driven without a platform.
	rawKey        func(code int, direction string)
	safetyTimeout time.Duration
}

func newHost(p *branchkit.Plugin) *Host {
	h := &Host{plugin: p, state: newPluginState()}
	h.rawKey = h.rawKeyDefault
	h.safetyTimeout = defaultSafetyTimeout
	return h
}
