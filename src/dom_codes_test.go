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

func TestParseDOMCode_NumpadPrefix(t *testing.T) {
	tests := map[string]string{
		"Numpad0":        "keypad_0",
		"Numpad9":        "keypad_9",
		"NumpadAdd":      "keypad_add",
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
		"Minus": "-", "Slash": "/", "Backslash": "\\",
		"BracketLeft": "[", "BracketRight": "]",
		"Semicolon": ";", "Quote": "'", "Comma": ",",
		"Period": ".", "Backquote": "`",
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
	if result.Combo != "/" {
		t.Errorf("combo = %q, want '/'", result.Combo)
	}
	if result.KeyName != "/" {
		t.Errorf("key_name = %q, want '/'", result.KeyName)
	}
}

func TestParseDOMKeyEvent_ShortcutWithPunctuation(t *testing.T) {
	result := parseDOMKeyEvent(DOMKeyEvent{
		Code: "Slash", Key: "/", MetaKey: true,
	})
	if result.Combo != "cmd+/" {
		t.Errorf("combo = %q, want 'cmd/'", result.Combo)
	}
}
