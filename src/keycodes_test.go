package main

import "testing"

func TestParseDOMCode_KeyPrefix(t *testing.T) {
	tests := map[string]string{
		"KeyA": "a", "KeyZ": "z", "KeyM": "m",
	}
	for code, want := range tests {
		if got := parseDOMCode(code); got != want {
			t.Errorf("parseDOMCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseDOMCode_DigitPrefix(t *testing.T) {
	tests := map[string]string{
		"Digit0": "0", "Digit5": "5", "Digit9": "9",
	}
	for code, want := range tests {
		if got := parseDOMCode(code); got != want {
			t.Errorf("parseDOMCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseDOMCode_Numpad(t *testing.T) {
	tests := map[string]string{
		"Numpad0": "keypad_0",
		"Numpad9": "keypad_9",
		// DOM suffix and registry name differ for these three.
		"NumpadAdd":      "keypad_plus",
		"NumpadSubtract": "keypad_minus",
		"NumpadEqual":    "keypad_equals",
		"NumpadMultiply": "keypad_multiply",
	}
	for code, want := range tests {
		if got := parseDOMCode(code); got != want {
			t.Errorf("parseDOMCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseDOMCode_MapEntries(t *testing.T) {
	tests := map[string]string{
		"Space": "space", "Enter": "return", "Tab": "tab",
		"Backspace": "delete", "Escape": "escape",
		"ArrowLeft": "left", "ArrowRight": "right",
		// Word names — the registry has no character-form aliases.
		"Minus": "minus", "Slash": "slash", "Backslash": "backslash",
		"BracketLeft": "leftbracket", "BracketRight": "rightbracket",
		"Semicolon": "semicolon", "Quote": "apostrophe", "Comma": "comma",
		"Period": "period", "Backquote": "backtick",
		"F1": "f1", "F12": "f12",
	}
	for code, want := range tests {
		if got := parseDOMCode(code); got != want {
			t.Errorf("parseDOMCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseDOMCode_Unknown(t *testing.T) {
	if got := parseDOMCode("UnknownCode"); got != "" {
		t.Errorf("parseDOMCode(UnknownCode) = %q, want empty", got)
	}
}

func TestParseDOMKeyEvent_SimpleKey(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{Code: "KeyA", Key: "a"})
	if result.Combo != "a" {
		t.Errorf("combo = %q, want 'a'", result.Combo)
	}
	if result.KeyName != "a" {
		t.Errorf("key_name = %q, want 'a'", result.KeyName)
	}
	if result.HasModifiers {
		t.Error("expected no modifiers")
	}
}

func TestParseDOMKeyEvent_WithModifiers(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{
		Code: "KeyV", Key: "v", AltKey: true, MetaKey: true,
	})
	if result.Combo != "opt+cmd+v" {
		t.Errorf("combo = %q, want 'opt+cmd+v'", result.Combo)
	}
	if !result.HasModifiers {
		t.Error("expected has_modifiers = true")
	}
}

func TestParseDOMKeyEvent_AllModifiers(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{
		Code: "KeyT", Key: "t",
		CtrlKey: true, AltKey: true, ShiftKey: true, MetaKey: true,
	})
	if result.Combo != "ctrl+opt+shift+cmd+t" {
		t.Errorf("combo = %q, want 'ctrl+opt+shift+cmd+t'", result.Combo)
	}
}

func TestParseDOMKeyEvent_Escape(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{Code: "Escape", Key: "Escape"})
	if !result.IsEscape {
		t.Error("expected is_escape = true")
	}
	if result.Combo != "" {
		t.Errorf("combo should be empty for escape, got %q", result.Combo)
	}
}

func TestParseDOMKeyEvent_BareModifier(t *testing.T) {
	for _, key := range []string{"Alt", "Shift", "Control", "Meta"} {
		result := parseDOMKeyEvent(DOMKeyEvent{Code: "AltLeft", Key: key})
		if !result.IsBareModifier {
			t.Errorf("key=%q: expected is_bare_modifier = true", key)
		}
	}
}

func TestParseDOMKeyEvent_UnknownCode(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{Code: "Unidentified", Key: "Unidentified"})
	if !result.IsBareModifier {
		t.Error("unknown code should be treated as bare modifier (ignored)")
	}
}

func TestParseDOMKeyEvent_Punctuation(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{Code: "Slash", Key: "/"})
	if result.Combo != "slash" {
		t.Errorf("combo = %q, want 'slash'", result.Combo)
	}
	if result.KeyName != "slash" {
		t.Errorf("key_name = %q, want 'slash'", result.KeyName)
	}
}

func TestParseDOMKeyEvent_ShortcutWithPunctuation(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{
		Code: "Slash", Key: "/", MetaKey: true,
	})
	if result.Combo != "cmd+slash" {
		t.Errorf("combo = %q, want 'cmd+slash'", result.Combo)
	}
}

// Guards the drift this map has already suffered once: the registry dropped
// its character-form aliases while domCodeMap went on emitting them, so every
// punctuation binding captured in the settings UI was unresolvable. A captured
// name the registry does not know is now dropped rather than stored.
func TestParseDOMKeyEvent_DropsNamesTheRegistryLacks(t *testing.T) {
	mu.Lock()
	saved := state.KeyNamesMerged
	state.KeyNamesMerged = map[string]uint16{"slash": 44, "a": 0}
	mu.Unlock()
	defer func() {
		mu.Lock()
		state.KeyNamesMerged = saved
		mu.Unlock()
	}()

	// "slash" is in the registry — kept.
	if got := parseDOMKeyEvent(DOMKeyEvent{Code: "Slash", Key: "/"}); got.KeyName != "slash" {
		t.Errorf("Slash key_name = %q, want slash", got.KeyName)
	}
	// "comma" is not in this registry — dropped, not stored as a dead binding.
	if got := parseDOMKeyEvent(DOMKeyEvent{Code: "Comma", Key: ","}); !got.IsBareModifier {
		t.Errorf("Comma should be dropped when absent from the registry, got %+v", got)
	}
}

// Before the registry loads there is nothing to check against, so the guard
// must not reject everything and leave capture broken at startup.
func TestParseDOMKeyEvent_EmptyRegistryDoesNotRejectEverything(t *testing.T) {
	mu.Lock()
	saved := state.KeyNamesMerged
	state.KeyNamesMerged = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		state.KeyNamesMerged = saved
		mu.Unlock()
	}()

	if got := parseDOMKeyEvent(DOMKeyEvent{Code: "Comma", Key: ","}); got.KeyName != "comma" {
		t.Errorf("with no registry loaded, key_name = %q, want comma", got.KeyName)
	}
}
