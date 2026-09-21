package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

// Binding is what a combo points at: an exact dotted action type plus its
// params — the same pair a command record's action carries. The actuator
// executes it as an ordinary Action through the shared executor; the
// string-routing dialect was deleted 2026-08-28. An override with an empty
// Action is a tombstone (the user unbound the combo).
type Binding struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (b Binding) IsZero() bool { return b.Action == "" }

// --- Key combo types ---

type KeyEvent int

const (
	KeyEventPress KeyEvent = iota
	KeyEventDown
	KeyEventUp
)

func (e KeyEvent) String() string {
	switch e {
	case KeyEventDown:
		return "down"
	case KeyEventUp:
		return "up"
	default:
		return "press"
	}
}

type Modifiers struct {
	Alt   bool
	Shift bool
	Ctrl  bool
	Cmd   bool
}

type KeyCombo struct {
	Key       string
	Modifiers Modifiers
	Event     KeyEvent
}

func (c KeyCombo) String() string {
	var parts []string
	if c.Modifiers.Ctrl {
		parts = append(parts, "ctrl")
	}
	if c.Modifiers.Alt {
		parts = append(parts, "opt")
	}
	if c.Modifiers.Shift {
		parts = append(parts, "shift")
	}
	if c.Modifiers.Cmd {
		parts = append(parts, "cmd")
	}
	parts = append(parts, c.Key)
	combo := strings.Join(parts, "+")
	if c.Event == KeyEventPress {
		return combo
	}
	return combo + " " + c.Event.String()
}

func parseCombo(s string) (KeyCombo, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return KeyCombo{}, false
	}

	// Split off trailing event keyword
	comboPart := s
	event := KeyEventPress
	words := strings.Fields(s)
	if len(words) >= 2 {
		last := strings.ToLower(words[len(words)-1])
		switch last {
		case "down":
			comboPart = strings.Join(words[:len(words)-1], " ")
			event = KeyEventDown
		case "up":
			comboPart = strings.Join(words[:len(words)-1], " ")
			event = KeyEventUp
		case "press":
			comboPart = strings.Join(words[:len(words)-1], " ")
			event = KeyEventPress
		}
	}

	tokens := strings.Split(comboPart, "+")
	for i := range tokens {
		tokens[i] = strings.TrimSpace(tokens[i])
	}
	if len(tokens) == 0 {
		return KeyCombo{}, false
	}

	key := strings.ToLower(tokens[len(tokens)-1])
	if key == "" {
		return KeyCombo{}, false
	}

	var mods Modifiers
	for _, tok := range tokens[:len(tokens)-1] {
		switch strings.ToLower(tok) {
		case "alt", "opt", "option":
			mods.Alt = true
		case "shift":
			mods.Shift = true
		case "ctrl", "control":
			mods.Ctrl = true
		case "cmd", "command", "meta":
			mods.Cmd = true
		default:
			return KeyCombo{}, false
		}
	}

	return KeyCombo{Key: key, Modifiers: mods, Event: event}, true
}

// comboKey returns a string key suitable for map lookups.
func comboKey(c KeyCombo) string {
	return c.String()
}

// modifierKeyID returns the base combo without event suffix (for listen_up).
func modifierKeyID(c KeyCombo) string {
	var parts []string
	if c.Modifiers.Alt {
		parts = append(parts, "alt+")
	}
	if c.Modifiers.Shift {
		parts = append(parts, "shift+")
	}
	if c.Modifiers.Ctrl {
		parts = append(parts, "ctrl+")
	}
	if c.Modifiers.Cmd {
		parts = append(parts, "cmd+")
	}
	return strings.Join(parts, "") + c.Key
}

// comboBaseString returns the combo without event type (for display).
func comboBaseString(c KeyCombo) string {
	var parts []string
	if c.Modifiers.Ctrl {
		parts = append(parts, "ctrl")
	}
	if c.Modifiers.Alt {
		parts = append(parts, "opt")
	}
	if c.Modifiers.Shift {
		parts = append(parts, "shift")
	}
	if c.Modifiers.Cmd {
		parts = append(parts, "cmd")
	}
	parts = append(parts, c.Key)
	return strings.Join(parts, "+")
}

// --- Registry ---

type KeybindSource struct {
	IsUser   bool
	PluginID string
}

func (s KeybindSource) String() string {
	if s.IsUser {
		return "user"
	}
	return "plugin:" + s.PluginID
}

type KeybindEntry struct {
	Combo  KeyCombo
	Action string
	Params json.RawMessage // nil for plain string actions
	Source KeybindSource
}

// ShadowedBind is a plugin binding that lost its combo to another plugin.
//
// Kept rather than discarded. Two plugins asking for one chord is a real
// situation the platform invites — `keybinds` is `writers:
// anyone_who_declares` — and the loser is a declaration the author wrote
// that now does nothing. Silence there is the failure: the author sees no
// error, the user sees no clash, and the binding is simply absent.
type ShadowedBind struct {
	Combo    KeyCombo
	Action   string
	PluginID string // the plugin whose binding did not take effect
	WonBy    string // the plugin holding the combo
}

type InternalRegistry struct {
	Entries  map[string]KeybindEntry // keyed by comboKey
	ListenUp map[string]bool
	// Plugin bindings that collided with an earlier one. Never registered
	// with the shell — they are reported, not applied.
	Shadowed []ShadowedBind
}

func newRegistry() InternalRegistry {
	return InternalRegistry{
		Entries:  make(map[string]KeybindEntry),
		ListenUp: make(map[string]bool),
	}
}

func (r *InternalRegistry) resolve(c KeyCombo) (KeybindEntry, bool) {
	if e, ok := r.Entries[comboKey(c)]; ok {
		return e, true
	}
	// Fall back: if looking for Down, try Press
	if c.Event == KeyEventDown {
		press := KeyCombo{Key: c.Key, Modifiers: c.Modifiers, Event: KeyEventPress}
		if e, ok := r.Entries[comboKey(press)]; ok {
			return e, true
		}
	}
	return KeybindEntry{}, false
}

// --- JSON interchange types ---

// Aliases, not mirrors — the same reasoning keycodes.go gives for
// ParsedKeyEvent: "a hand-written mirror of a platform shape zero-fills
// silently when the platform renames a field; this breaks the build
// instead." These two WERE mirrors, byte-identical to the generated
// shapes, until 2026-09-20. keybinds.register takes the generated type,
// so aliasing also lets the call site use the wrapper.
type RegistrySnapshot = branchkit.RegistrySnapshot
type RegistryEntry = branchkit.RegistryEntry

func (r *InternalRegistry) toSnapshot() RegistrySnapshot {
	entries := make([]RegistryEntry, 0, len(r.Entries))
	for _, e := range r.Entries {
		entries = append(entries, RegistryEntry{
			Combo:  e.Combo.String(),
			Action: e.Action,
			Source: e.Source.String(),
			Params: e.Params,
		})
	}
	listenUp := make([]string, 0, len(r.ListenUp))
	for k := range r.ListenUp {
		listenUp = append(listenUp, k)
	}
	return RegistrySnapshot{Entries: entries, ListenUp: listenUp}
}

// --- User overrides ---

// Var seams so handler tests can run the real remap/reset flows without a
// live actuator behind plugin.Call.
func (h *Host) loadUserKeybindOverridesDefault() map[string]Binding {
	return h.loadOverridesFromCollection()
}

func (h *Host) saveUserKeybindOverridesDefault(overrides map[string]Binding) {
	h.saveOverridesToCollection(overrides)
}

// --- Registry build ---

func (h *Host) buildRegistry(
	keybindsByPlugin map[string]map[string]Binding,
) InternalRegistry {
	reg := newRegistry()

	// 1. Collect from plugins (sorted alphabetically, first wins)
	pluginIDs := make([]string, 0, len(keybindsByPlugin))
	for id := range keybindsByPlugin {
		pluginIDs = append(pluginIDs, id)
	}
	sort.Strings(pluginIDs)

	for _, pluginID := range pluginIDs {
		keybinds := keybindsByPlugin[pluginID]
		for comboStr, b := range keybinds {
			combo, ok := parseCombo(comboStr)
			if !ok {
				continue
			}
			key := comboKey(combo)
			if existing, exists := reg.Entries[key]; exists {
				// First plugin alphabetically wins. That is deterministic,
				// which matters more than it sounds — but it is arbitrary,
				// so the one that lost has to be visible somewhere rather
				// than vanishing. Within this loop every existing entry is
				// plugin-sourced; user overrides are applied in step 2.
				reg.Shadowed = append(reg.Shadowed, ShadowedBind{
					Combo:    combo,
					Action:   b.Action,
					PluginID: pluginID,
					WonBy:    existing.Source.PluginID,
				})
				branchkit.Logf("keyboard",
					"keybind %s: %s wanted %q but %s holds it — first plugin alphabetically wins; rebind one of them in Settings",
					combo.String(), pluginID, b.Action, existing.Source.PluginID)
				continue
			}
			reg.Entries[key] = KeybindEntry{
				Combo:  combo,
				Action: b.Action,
				Params: b.Params,
				Source: KeybindSource{PluginID: pluginID},
			}
		}
	}

	// 2. User TOML overrides (always win)
	userOverrides := h.loadUserKeybindOverrides()
	for comboStr, b := range userOverrides {
		combo, ok := parseCombo(comboStr)
		if !ok {
			continue
		}
		key := comboKey(combo)
		if b.IsZero() {
			delete(reg.Entries, key)
		} else {
			reg.Entries[key] = KeybindEntry{
				Combo:  combo,
				Action: b.Action,
				Params: b.Params,
				Source: KeybindSource{IsUser: true},
			}
		}
	}

	// 3. Build listen_up set
	for _, e := range reg.Entries {
		if e.Combo.Event == KeyEventUp {
			reg.ListenUp[modifierKeyID(e.Combo)] = true
		}
	}

	return reg
}
