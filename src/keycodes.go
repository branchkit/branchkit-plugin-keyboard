package main

import (
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

// domCodeMap maps DOM KeyboardEvent.code values to BranchKit key names.
// Handles non-alphanumeric keys; Key* and Digit* prefixes are stripped
// programmatically.
//
// Names here MUST exist in the `_platform.key_names` registry, or a combo
// captured in the settings UI produces something no adapter can resolve.
// The registry is word-only — the character-form aliases ("-", "/", …) were
// dropped from it, and this map went on emitting them.
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
	"Minus":        "minus",
	"Equal":        "equals",
	"BracketLeft":  "leftbracket",
	"BracketRight": "rightbracket",
	"Backslash":    "backslash",
	"Semicolon":    "semicolon",
	"Quote":        "apostrophe",
	"Comma":        "comma",
	"Period":       "period",
	"Slash":        "slash",
	"Backquote":    "backtick",
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

	// Numpad is explicit rather than "keypad_" + lowercase suffix: three DOM
	// suffixes do not match the registry's names (Subtract/Add/Equal vs
	// minus/plus/equals), and NumpadComma has no keycode at all, so the
	// prefix rule minted names nothing could resolve.
	"Numpad0":        "keypad_0",
	"Numpad1":        "keypad_1",
	"Numpad2":        "keypad_2",
	"Numpad3":        "keypad_3",
	"Numpad4":        "keypad_4",
	"Numpad5":        "keypad_5",
	"Numpad6":        "keypad_6",
	"Numpad7":        "keypad_7",
	"Numpad8":        "keypad_8",
	"Numpad9":        "keypad_9",
	"NumpadClear":    "keypad_clear",
	"NumpadDecimal":  "keypad_decimal",
	"NumpadDivide":   "keypad_divide",
	"NumpadEnter":    "keypad_enter",
	"NumpadMultiply": "keypad_multiply",
	"NumpadSubtract": "keypad_minus",
	"NumpadAdd":      "keypad_plus",
	"NumpadEqual":    "keypad_equals",
}

// parseDOMCode converts a DOM KeyboardEvent.code to a BranchKit key name.
// Returns empty string if the code is unrecognized.
func parseDOMCode(code string) string {
	// The explicit map wins over the prefix rules, so it can never mint a
	// name the registry does not have.
	if name, ok := domCodeMap[code]; ok {
		return name
	}
	if strings.HasPrefix(code, "Key") {
		return strings.ToLower(code[3:])
	}
	if strings.HasPrefix(code, "Digit") {
		return code[5:]
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

// keyNameIsResolvable reports whether the registry knows this name. Returns
// true when the registry has not loaded yet, so this can only ever reject a
// name we positively know is bad.
func keyNameIsResolvable(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	if len(state.KeyNamesMerged) == 0 {
		return true
	}
	_, ok := state.KeyNamesMerged[strings.ToLower(name)]
	return ok
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
	// Last line of defence against this map drifting from the key-name
	// registry again: a captured combo must be a name something can resolve,
	// or the binding is dead on arrival. Only enforced once the registry has
	// actually loaded — before that we have nothing to check against.
	if !keyNameIsResolvable(keyName) {
		branchkit.Logf("keyboard",
			"dropping captured key %q (DOM code %q): not in the key-name registry",
			keyName, evt.Code)
		return ParsedKeyEvent{IsBareModifier: true}
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
