package main

import (
	"sync"
	"sync/atomic"

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

	// registerKeybinds pushes a rebuilt snapshot to the platform. A field so
	// handler tests can assert the registration happens without a platform.
	registerKeybinds         func(RegistrySnapshot)
	fetchBindableCommands    func() ([]bindCandidate, error)
	loadUserKeybindOverrides func() map[string]Binding
	saveUserKeybindOverrides func(overrides map[string]Binding)
	parseKeyEvent            func(ev DOMKeyEvent) (ParsedKeyEvent, error)
}

func newHost(p *branchkit.Plugin) *Host {
	h := &Host{plugin: p, state: newPluginState()}
	h.registerKeybinds = h.registerKeybindsDefault
	h.fetchBindableCommands = h.fetchBindableCommandsDefault
	h.loadUserKeybindOverrides = h.loadUserKeybindOverridesDefault
	h.saveUserKeybindOverrides = h.saveUserKeybindOverridesDefault
	h.parseKeyEvent = h.parseKeyEventDefault
	return h
}
