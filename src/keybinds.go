package main

import (
	"encoding/json"
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
	// Toggle and abort are the platform's other two event words: a toggle
	// alternates start/stop on each press, an abort abandons a held action.
	KeyEventToggle
	KeyEventAbort
)

func (e KeyEvent) String() string {
	switch e {
	case KeyEventDown:
		return "down"
	case KeyEventUp:
		return "up"
	case KeyEventToggle:
		return "toggle"
	case KeyEventAbort:
		return "abort"
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
		case "toggle":
			comboPart = strings.Join(words[:len(words)-1], " ")
			event = KeyEventToggle
		case "abort":
			comboPart = strings.Join(words[:len(words)-1], " ")
			event = KeyEventAbort
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
//
// The platform derives the hotkey table and publishes it as
// `_platform.bindings.active`; this plugin reads it to show and edit. It used
// to build the table itself and push it with `keybinds.register`, which let
// any plugin replace every hotkey on the machine, and meant an edit never
// reached the shell while this plugin was stopped. The rules did not change
// in the move: plugin bindings by plugin id, first wins; the user's edits on
// top; a binding that lost its combo kept and shown.

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
// situation the platform invites — any plugin may contribute bindings — and
// the loser is a declaration the author wrote that now does nothing. Silence
// there is the failure: the author sees no error, the user sees no clash,
// and the binding is simply absent.
type ShadowedBind struct {
	Combo    KeyCombo
	Action   string
	PluginID string // the plugin whose binding did not take effect
	WonBy    string // the plugin holding the combo
}

type InternalRegistry struct {
	Entries map[string]KeybindEntry // keyed by comboKey
	// Plugin bindings that collided with an earlier one. The platform never
	// registers them — they are reported, not applied.
	Shadowed []ShadowedBind
}

func newRegistry() InternalRegistry {
	return InternalRegistry{
		Entries: make(map[string]KeybindEntry),
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

// activeTable is the one record of `_platform.bindings.active`.
type activeTable struct {
	Entries []struct {
		Trigger  string          `json:"trigger"`
		Action   string          `json:"action"`
		Params   json.RawMessage `json:"params,omitempty"`
		ByUser   bool            `json:"by_user"`   // the user's edit
		PluginID string          `json:"plugin_id"` // else the contributing plugin
	} `json:"entries"`
	Shadowed []struct {
		Trigger  string `json:"trigger"`
		Action   string `json:"action"`
		PluginID string `json:"plugin_id"`
		WonBy    string `json:"won_by"`
	} `json:"shadowed"`
}

const bindingsActiveCollection = "_platform.bindings.active"

// registryFromActive turns the platform's published table into the shape the
// Keybinds tab renders. A trigger this plugin cannot parse is skipped: the
// platform parsed it to register it, so a miss here is display-only.
func registryFromActive(t activeTable) InternalRegistry {
	reg := newRegistry()
	for _, e := range t.Entries {
		combo, ok := parseCombo(e.Trigger)
		if !ok {
			branchkit.Logf("keyboard", "active binding %q: cannot display this trigger", e.Trigger)
			continue
		}
		reg.Entries[comboKey(combo)] = KeybindEntry{
			Combo:  combo,
			Action: e.Action,
			Params: nonNullParams(e.Params),
			Source: KeybindSource{IsUser: e.ByUser, PluginID: e.PluginID},
		}
	}
	for _, s := range t.Shadowed {
		combo, ok := parseCombo(s.Trigger)
		if !ok {
			continue
		}
		reg.Shadowed = append(reg.Shadowed, ShadowedBind{
			Combo: combo, Action: s.Action, PluginID: s.PluginID, WonBy: s.WonBy,
		})
	}
	return reg
}

func nonNullParams(p json.RawMessage) json.RawMessage {
	if len(p) == 0 || string(p) == "null" {
		return nil
	}
	return p
}

func (h *Host) fetchActiveDefault() (activeTable, error) {
	var t activeTable
	rec, err := h.plugin.Get(bindingsActiveCollection, "singleton")
	if err != nil || rec == nil {
		return t, err
	}
	err = json.Unmarshal(rec.Payload, &t)
	return t, err
}

// contributedBinding is one plugin's binding as `_platform.bindings` holds it.
type contributedBinding struct {
	Trigger string
	Binding
}

func (h *Host) fetchContributedDefault() ([]contributedBinding, error) {
	recs, err := h.plugin.ListAll(bindingsCollection)
	if err != nil {
		return nil, err
	}
	out := make([]contributedBinding, 0, len(recs))
	for _, r := range recs {
		var p struct {
			Trigger string          `json:"trigger"`
			Action  string          `json:"action"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if json.Unmarshal(r.Payload, &p) != nil {
			continue
		}
		out = append(out, contributedBinding{Trigger: p.Trigger, Binding: Binding{Action: p.Action, Params: nonNullParams(p.Params)}})
	}
	return out, nil
}

const bindingsCollection = "_platform.bindings"

// pluginBinds reports whether any plugin contributed a binding of action on
// trigger — shadowed ones included, as the per-plugin lookup it replaces did.
func (h *Host) pluginBinds(trigger, action string) bool {
	want, ok := parseCombo(trigger)
	if !ok {
		return false
	}
	contributed, err := h.fetchContributed()
	if err != nil {
		branchkit.Logf("keyboard", "reading %s: %v", bindingsCollection, err)
		return false
	}
	for _, c := range contributed {
		if got, ok := parseCombo(c.Trigger); ok && comboKey(got) == comboKey(want) && c.Action == action {
			return true
		}
	}
	return false
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
