package main

import (
	"context"
	"strings"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

type repeatConfig struct {
	InitialDelay   time.Duration
	RepeatInterval time.Duration
}

// keyTarget is a key to hold: a name when the caller gave one (the usual
// case), plus the code it resolved to. The name matters because modifier
// CLASSIFICATION cannot be done on a raw code — the platform registry is
// per-OS, so macOS's cmd (55) is `v` on Linux and Numpad* on Windows.
type keyTarget struct {
	name string
	code int
}

type holdState struct {
	id     uint64
	cancel context.CancelFunc
	key    keyTarget
	mods   []string
}

func (h *Host) isModifierKey(t keyTarget) bool { return h.modifierNameForKey(t) != "" }

// modifierNameForKey returns the canonical modifier name for a target, or ""
// if it is not a modifier. Classification is by NAME: a raw code cannot say,
// because the registry is per-OS (macOS cmd=55 is `v` on Linux, Numpad* on
// Windows). A code-only target is reverse-looked-up in the registry first.
func (h *Host) modifierNameForKey(t keyTarget) string {
	if t.name != "" {
		return canonicalModifier(t.name)
	}
	for _, n := range h.namesForCode(t.code) {
		if m := canonicalModifier(n); m != "" {
			return m
		}
	}
	return ""
}

// canonicalModifier folds the modifier aliases onto one name, or "" if the
// name is not a modifier.
func canonicalModifier(name string) string {
	switch strings.ToLower(name) {
	case "cmd", "command", "meta", "right_cmd", "right_command":
		return "cmd"
	case "shift", "right_shift":
		return "shift"
	case "option", "opt", "alt", "right_option":
		return "opt"
	case "ctrl", "control", "right_ctrl", "right_control":
		return "ctrl"
	}
	return ""
}

// namesForCode reverse-looks-up the registry. Codes are not unique (aliases
// share one), so this returns every name that maps to it.
func (h *Host) namesForCode(code int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for n, c := range h.state.KeyNamesMerged {
		if int(c) == code {
			out = append(out, n)
		}
	}
	return out
}

// activeModifiers returns any modifiers currently held via hold mode.
// Called by action handlers to inject held modifiers into key actions.
func (h *Host) activeModifiers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.heldModifiers) == 0 {
		return nil
	}
	result := make([]string, len(h.heldModifiers))
	copy(result, h.heldModifiers)
	return result
}

const safetyTimeout = 30 * time.Second

func loadRepeatConfig(p *branchkit.Plugin) repeatConfig {
	cfg := repeatConfig{
		InitialDelay:   500 * time.Millisecond,
		RepeatInterval: 33 * time.Millisecond,
	}

	// These were the last two raw calls in this plugin, held back because a
	// bare-f64 result generated as `() error` and discarded the number. The
	// emitter renders a scalar result as `(float64, error)` since
	// 2026-09-20, so they read through the wrapper now.
	if delay, err := p.NativeKeyRepeatDelay(); err == nil && delay > 0 {
		cfg.InitialDelay = time.Duration(delay * float64(time.Second))
	}

	if rate, err := p.NativeKeyRepeatRate(); err == nil && rate > 0 {
		cfg.RepeatInterval = time.Duration(float64(time.Second) / rate)
	}

	branchkit.Logf("keyboard", "repeat config: delay=%v interval=%v", cfg.InitialDelay, cfg.RepeatInterval)
	return cfg
}

func (h *Host) resolveKeyCode(name string) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	code, ok := h.state.KeyNamesMerged[strings.ToLower(name)]
	if ok {
		return int(code), true
	}
	return 0, false
}

func (h *Host) pressRawKey(code int, direction string) {
	logErr("repeat.raw_key", h.plugin.InputRawKey(code, direction))
}

func (h *Host) startHold(t keyTarget, mods []string, repeat bool) {
	// Modifier keys are tracked virtually — they get injected into
	// subsequent key actions rather than sent as raw events (which
	// would be undone by the actuator's lift_modifiers).
	if h.isModifierKey(t) {
		modName := h.modifierNameForKey(t)
		h.mu.Lock()
		h.heldModifiers = append(h.heldModifiers, modName)
		h.mu.Unlock()
		branchkit.Logf("keyboard", "hold modifier: %s", modName)
		return
	}

	h.mu.Lock()
	prev := h.activeHold
	id := h.holdSeq.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	h.activeHold = &holdState{id: id, cancel: cancel, key: t, mods: mods}
	h.mu.Unlock()

	if prev != nil {
		prev.cancel()
		h.releaseKeys(prev.key, prev.mods)
	}

	h.pressModifiers(mods, "press")
	h.pressRawKey(t.code, "press")

	if repeat {
		go h.runRepeatLoop(ctx, id, t)
	}
}

func (h *Host) stopHold(t keyTarget, mods []string) {
	if h.isModifierKey(t) {
		modName := h.modifierNameForKey(t)
		h.mu.Lock()
		for i, m := range h.heldModifiers {
			if m == modName {
				h.heldModifiers = append(h.heldModifiers[:i], h.heldModifiers[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		branchkit.Logf("keyboard", "release modifier: %s", modName)
		return
	}

	h.mu.Lock()
	hold := h.activeHold
	if hold != nil {
		h.activeHold = nil
	}
	h.mu.Unlock()

	if hold != nil {
		hold.cancel()
	}

	h.pressRawKey(t.code, "release")
	h.pressModifiers(mods, "release")
}

func (h *Host) releaseKeys(t keyTarget, mods []string) {
	h.pressRawKey(t.code, "release")
	h.pressModifiers(mods, "release")
}

// pressModifiers injects modifier keys by resolving their names through the
// platform registry, so the codes are right on every OS. It used to carry its
// own macOS keycode table, which injected `v` for cmd on Linux.
func (h *Host) pressModifiers(mods []string, direction string) {
	for _, m := range mods {
		if canonicalModifier(m) == "" {
			continue
		}
		if code, ok := h.resolveKeyCode(m); ok {
			h.pressRawKey(code, direction)
		}
	}
}

func (h *Host) runRepeatLoop(ctx context.Context, id uint64, t keyTarget) {
	timer := time.NewTimer(h.repeatCfg.InitialDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(h.repeatCfg.RepeatInterval)
	defer ticker.Stop()

	deadline := time.After(safetyTimeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			h.mu.Lock()
			if h.activeHold != nil && h.activeHold.id == id {
				hold := h.activeHold
				h.activeHold = nil
				h.mu.Unlock()
				h.releaseKeys(hold.key, hold.mods)
			} else {
				h.mu.Unlock()
			}
			return
		case <-ticker.C:
			h.pressRawKey(t.code, "click")
		}
	}
}
