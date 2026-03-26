package main

import (
	"strings"
)

// domCodeMap maps DOM KeyboardEvent.code values to BranchKit key names.
// Handles non-alphanumeric keys; Key*, Digit*, Numpad* prefixes are stripped programmatically.
var domCodeMap = map[string]string{
	"Space":        "space",
	"Enter":        "return",
	"Tab":          "tab",
	"Backspace":    "delete",
	"Delete":       "forward_delete",
	"Escape":       "escape",
	"CapsLock":     "capslock",
	"ArrowLeft":    "left",
	"ArrowRight":   "right",
	"ArrowDown":    "down",
	"ArrowUp":      "up",
	"PageUp":       "pageup",
	"PageDown":     "pagedown",
	"Home":         "home",
	"End":          "end",
	"Minus":        "-",
	"Equal":        "=",
	"BracketLeft":  "[",
	"BracketRight": "]",
	"Backslash":    "\\",
	"Semicolon":    ";",
	"Quote":        "'",
	"Comma":        ",",
	"Period":       ".",
	"Slash":        "/",
	"Backquote":    "`",
	"F1":           "f1",
	"F2":           "f2",
	"F3":           "f3",
	"F4":           "f4",
	"F5":           "f5",
	"F6":           "f6",
	"F7":           "f7",
	"F8":           "f8",
	"F9":           "f9",
	"F10":          "f10",
	"F11":          "f11",
	"F12":          "f12",
	"F13":          "f13",
	"F14":          "f14",
	"F15":          "f15",
	"F16":          "f16",
	"F17":          "f17",
	"F18":          "f18",
	"F19":          "f19",
	"F20":          "f20",
}

// parseDOMCode converts a DOM KeyboardEvent.code to a BranchKit key name.
// Returns empty string if the code is unrecognized.
func parseDOMCode(code string) string {
	if strings.HasPrefix(code, "Key") {
		return strings.ToLower(code[3:])
	}
	if strings.HasPrefix(code, "Digit") {
		return code[5:]
	}
	if strings.HasPrefix(code, "Numpad") {
		return "keypad_" + strings.ToLower(code[6:])
	}
	if name, ok := domCodeMap[code]; ok {
		return name
	}
	return ""
}

// DOMKeyEvent represents the relevant fields from a DOM KeyboardEvent.
type DOMKeyEvent struct {
	Code     string `json:"code"`
	Key      string `json:"key"`
	CtrlKey  bool   `json:"ctrl"`
	AltKey   bool   `json:"alt"`
	ShiftKey bool   `json:"shift"`
	MetaKey  bool   `json:"meta"`
}

// ParsedKeyEvent is the result of parsing a DOMKeyEvent.
type ParsedKeyEvent struct {
	Combo          string `json:"combo"`
	KeyName        string `json:"key_name"`
	HasModifiers   bool   `json:"has_modifiers"`
	IsBareModifier bool   `json:"is_bare_modifier"`
	IsEscape       bool   `json:"is_escape"`
}

// isBareModifierKey returns true if the DOM Key value is a modifier-only key.
func isBareModifierKey(key string) bool {
	switch key {
	case "Alt", "Shift", "Control", "Meta":
		return true
	}
	return false
}

// parseDOMKeyEvent converts a DOMKeyEvent into a ParsedKeyEvent.
func parseDOMKeyEvent(evt DOMKeyEvent) ParsedKeyEvent {
	if evt.Key == "Escape" {
		return ParsedKeyEvent{IsEscape: true}
	}

	if isBareModifierKey(evt.Key) {
		return ParsedKeyEvent{IsBareModifier: true}
	}

	keyName := parseDOMCode(evt.Code)
	if keyName == "" {
		return ParsedKeyEvent{IsBareModifier: true} // treat unknown as ignore
	}

	var mods []string
	if evt.CtrlKey {
		mods = append(mods, "ctrl")
	}
	if evt.AltKey {
		mods = append(mods, "opt")
	}
	if evt.ShiftKey {
		mods = append(mods, "shift")
	}
	if evt.MetaKey {
		mods = append(mods, "cmd")
	}

	hasModifiers := len(mods) > 0
	combo := keyName
	if hasModifiers {
		combo = strings.Join(mods, "+") + "+" + keyName
	}

	return ParsedKeyEvent{
		Combo:        combo,
		KeyName:      keyName,
		HasModifiers: hasModifiers,
	}
}

// handleParseKeyEvent exposes key event parsing for cross-plugin use.
func handleParseKeyEvent(evt *DOMKeyEvent) (any, error) {
	return parseDOMKeyEvent(*evt), nil
}
