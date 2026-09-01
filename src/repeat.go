package main

import (
	"context"
	"strings"
	"sync/atomic"
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

var (
	activeHold    *holdState
	holdSeq       atomic.Uint64
	repeatCfg     repeatConfig
	heldModifiers []string // modifier names held via hold mode (injected into key actions)
)

func isModifierKey(t keyTarget) bool { return modifierNameForKey(t) != "" }

// modifierNameForKey returns the canonical modifier name for a target, or ""
// if it is not a modifier. Classification is by NAME: a raw code cannot say,
// because the registry is per-OS (macOS cmd=55 is `v` on Linux, Numpad* on
// Windows). A code-only target is reverse-looked-up in the registry first.
func modifierNameForKey(t keyTarget) string {
	if t.name != "" {
		return canonicalModifier(t.name)
	}
	for _, n := range namesForCode(t.code) {
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
func namesForCode(code int) []string {
	mu.Lock()
	defer mu.Unlock()
	var out []string
	for n, c := range state.KeyNamesMerged {
		if int(c) == code {
			out = append(out, n)
		}
	}
	return out
}

// activeModifiers returns any modifiers currently held via hold mode.
// Called by action handlers to inject held modifiers into key actions.
func activeModifiers() []string {
	mu.Lock()
	defer mu.Unlock()
	if len(heldModifiers) == 0 {
		return nil
	}
	result := make([]string, len(heldModifiers))
	copy(result, heldModifiers)
	return result
}

const safetyTimeout = 30 * time.Second

func loadRepeatConfig(p *branchkit.Plugin) repeatConfig {
	cfg := repeatConfig{
		InitialDelay:   500 * time.Millisecond,
		RepeatInterval: 33 * time.Millisecond,
	}

	var delay float64
	if err := p.Call("native.key_repeat_delay", nil, &delay); err == nil && delay > 0 {
		cfg.InitialDelay = time.Duration(delay * float64(time.Second))
	}

	var rate float64
	if err := p.Call("native.key_repeat_rate", nil, &rate); err == nil && rate > 0 {
		cfg.RepeatInterval = time.Duration(float64(time.Second) / rate)
	}

	branchkit.Logf("keyboard", "repeat config: delay=%v interval=%v", cfg.InitialDelay, cfg.RepeatInterval)
	return cfg
}

func resolveKeyCode(name string) (int, bool) {
	mu.Lock()
	defer mu.Unlock()
	code, ok := state.KeyNamesMerged[strings.ToLower(name)]
	if ok {
		return int(code), true
	}
	return 0, false
}

func pressRawKey(code int, direction string) {
	logErr("repeat.raw_key", plugin.Call("input.raw_key", map[string]any{
		"code":      code,
		"direction": direction,
	}, nil))
}

func startHold(t keyTarget, mods []string, repeat bool) {
	// Modifier keys are tracked virtually — they get injected into
	// subsequent key actions rather than sent as raw events (which
	// would be undone by the actuator's lift_modifiers).
	if isModifierKey(t) {
		modName := modifierNameForKey(t)
		mu.Lock()
		heldModifiers = append(heldModifiers, modName)
		mu.Unlock()
		branchkit.Logf("keyboard", "hold modifier: %s", modName)
		return
	}

	mu.Lock()
	prev := activeHold
	id := holdSeq.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	activeHold = &holdState{id: id, cancel: cancel, key: t, mods: mods}
	mu.Unlock()

	if prev != nil {
		prev.cancel()
		releaseKeys(prev.key, prev.mods)
	}

	pressModifiers(mods, "press")
	pressRawKey(t.code, "press")

	if repeat {
		go runRepeatLoop(ctx, id, t)
	}
}

func stopHold(t keyTarget, mods []string) {
	if isModifierKey(t) {
		modName := modifierNameForKey(t)
		mu.Lock()
		for i, m := range heldModifiers {
			if m == modName {
				heldModifiers = append(heldModifiers[:i], heldModifiers[i+1:]...)
				break
			}
		}
		mu.Unlock()
		branchkit.Logf("keyboard", "release modifier: %s", modName)
		return
	}

	mu.Lock()
	h := activeHold
	if h != nil {
		activeHold = nil
	}
	mu.Unlock()

	if h != nil {
		h.cancel()
	}

	pressRawKey(t.code, "release")
	pressModifiers(mods, "release")
}

func releaseKeys(t keyTarget, mods []string) {
	pressRawKey(t.code, "release")
	pressModifiers(mods, "release")
}

// pressModifiers injects modifier keys by resolving their names through the
// platform registry, so the codes are right on every OS. It used to carry its
// own macOS keycode table, which injected `v` for cmd on Linux.
func pressModifiers(mods []string, direction string) {
	for _, m := range mods {
		if canonicalModifier(m) == "" {
			continue
		}
		if code, ok := resolveKeyCode(m); ok {
			pressRawKey(code, direction)
		}
	}
}

func runRepeatLoop(ctx context.Context, id uint64, t keyTarget) {
	timer := time.NewTimer(repeatCfg.InitialDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(repeatCfg.RepeatInterval)
	defer ticker.Stop()

	deadline := time.After(safetyTimeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			mu.Lock()
			if activeHold != nil && activeHold.id == id {
				h := activeHold
				activeHold = nil
				mu.Unlock()
				releaseKeys(h.key, h.mods)
			} else {
				mu.Unlock()
			}
			return
		case <-ticker.C:
			pressRawKey(t.code, "click")
		}
	}
}
