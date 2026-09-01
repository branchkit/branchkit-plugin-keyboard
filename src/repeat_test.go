package main

import "testing"

func TestCanonicalModifier(t *testing.T) {
	for _, in := range []string{"cmd", "command", "meta", "right_cmd", "right_command", "CMD"} {
		if got := canonicalModifier(in); got != "cmd" {
			t.Errorf("canonicalModifier(%q) = %q, want cmd", in, got)
		}
	}
	for _, in := range []string{"opt", "option", "alt", "right_option"} {
		if got := canonicalModifier(in); got != "opt" {
			t.Errorf("canonicalModifier(%q) = %q, want opt", in, got)
		}
	}
	for _, in := range []string{"ctrl", "control", "right_ctrl", "right_control"} {
		if got := canonicalModifier(in); got != "ctrl" {
			t.Errorf("canonicalModifier(%q) = %q, want ctrl", in, got)
		}
	}
	for _, in := range []string{"escape", "a", "", "fn", "v"} {
		if got := canonicalModifier(in); got != "" {
			t.Errorf("canonicalModifier(%q) = %q, want empty", in, got)
		}
	}
}

// The bug this guards: classification used to compare the RESOLVED CODE
// against hardcoded macOS keycodes (cmd=55, shift=56, …). The key-name
// registry is per-OS, so on Linux code 55 is `v` and code 59 is `comma` —
// holding either was silently swallowed as a held modifier instead of being
// pressed. Classify by name.
func TestModifierNameForKey_NamedBeatsCode(t *testing.T) {
	mu.Lock()
	state.KeyNamesMerged = map[string]uint16{
		"v": 55, "comma": 59, "b": 56, // the Linux X11 collisions
		"cmd": 133, "shift": 50, "ctrl": 37,
	}
	mu.Unlock()

	for _, name := range []string{"v", "comma", "b"} {
		if got := modifierNameForKey(keyTarget{name: name, code: int(state.KeyNamesMerged[name])}); got != "" {
			t.Errorf("%q resolved to code %d must NOT be a modifier, got %q",
				name, state.KeyNamesMerged[name], got)
		}
	}
	if got := modifierNameForKey(keyTarget{name: "cmd", code: 133}); got != "cmd" {
		t.Errorf("named cmd = %q, want cmd", got)
	}
	if got := modifierNameForKey(keyTarget{name: "shift", code: 50}); got != "shift" {
		t.Errorf("named shift = %q, want shift", got)
	}
}

// A code-only target (the raw-keycode actions) has no name, so it is reverse
// looked up in the registry rather than compared to macOS constants.
func TestModifierNameForKey_CodeReverseLookup(t *testing.T) {
	mu.Lock()
	state.KeyNamesMerged = map[string]uint16{"v": 55, "cmd": 133, "ctrl": 37}
	mu.Unlock()

	if got := modifierNameForKey(keyTarget{code: 133}); got != "cmd" {
		t.Errorf("code 133 = %q, want cmd", got)
	}
	if got := modifierNameForKey(keyTarget{code: 55}); got != "" {
		t.Errorf("code 55 (v on this registry) = %q, want empty", got)
	}
	if got := modifierNameForKey(keyTarget{code: 9999}); got != "" {
		t.Errorf("unknown code = %q, want empty", got)
	}
}
