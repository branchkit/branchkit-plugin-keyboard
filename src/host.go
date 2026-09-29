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

	// registerKeybinds pushes a rebuilt snapshot to the platform. A field so
	// handler tests can assert the registration happens without a platform.
	registerKeybinds         func(RegistrySnapshot)
	fetchBindableCommands    func() ([]bindCandidate, error)
	loadUserKeybindOverrides func() map[string]Binding
	saveUserKeybindOverrides func(overrides map[string]Binding)
	// overridesUnreadable is set when reading the overrides record FAILED
	// (not when it was absent or unparseable). Every remap is load, edit,
	// save the whole map, so saving after a failed read would replace the
	// user's saved overrides with just the one edit. Saves refuse while it
	// is set; the next successful read clears it.
	overridesUnreadable atomic.Bool
	parseKeyEvent       func(ev DOMKeyEvent) (ParsedKeyEvent, error)

	// The hotkey pause a key capture holds (pauseKeybinds), as seams so a
	// test can watch it, and the timer that bounds it (captureTimeout).
	holdPause    func()
	releasePause func()
	captureMu    sync.Mutex
	captureTimer *time.Timer
}

func newHost(p *branchkit.Plugin) *Host {
	h := &Host{plugin: p, state: newPluginState()}
	h.registerKeybinds = h.registerKeybindsDefault
	h.fetchBindableCommands = h.fetchBindableCommandsDefault
	h.loadUserKeybindOverrides = h.loadUserKeybindOverridesDefault
	h.saveUserKeybindOverrides = h.saveUserKeybindOverridesDefault
	h.parseKeyEvent = h.parseKeyEventDefault
	h.holdPause = h.holdPauseDefault
	h.releasePause = h.releasePauseDefault
	h.rawKey = h.rawKeyDefault
	h.safetyTimeout = defaultSafetyTimeout
	return h
}
