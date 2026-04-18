package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/branchkit/plugin-sdk-go"
)

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
	Source KeybindSource
}

type InternalRegistry struct {
	Entries  map[string]KeybindEntry // keyed by comboKey
	ListenUp map[string]bool
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

type RegistrySnapshot struct {
	Entries  []RegistryEntry `json:"entries"`
	ListenUp []string        `json:"listen_up"`
}

type RegistryEntry struct {
	Combo  string `json:"combo"`
	Action string `json:"action"`
	Source string `json:"source"`
}

func (r *InternalRegistry) toSnapshot() RegistrySnapshot {
	entries := make([]RegistryEntry, 0, len(r.Entries))
	for _, e := range r.Entries {
		entries = append(entries, RegistryEntry{
			Combo:  e.Combo.String(),
			Action: e.Action,
			Source: e.Source.String(),
		})
	}
	listenUp := make([]string, 0, len(r.ListenUp))
	for k := range r.ListenUp {
		listenUp = append(listenUp, k)
	}
	return RegistrySnapshot{Entries: entries, ListenUp: listenUp}
}

// --- TOML overrides ---

func loadUserKeybindOverrides(path string) map[string]string {
	if path == "" {
		return make(map[string]string)
	}
	return loadOverridesFromTOML(path)
}

func saveUserKeybindOverrides(overrides map[string]string, path string) {
	if path == "" {
		return
	}
	saveOverridesToTOML(overrides, path)
}

// --- Registry build ---

func buildRegistry(
	keybindsByPlugin map[string]map[string]string,
	overridesTomlPath string,
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
		for comboStr, action := range keybinds {
			combo, ok := parseCombo(comboStr)
			if !ok {
				continue
			}
			key := comboKey(combo)
			if _, exists := reg.Entries[key]; exists {
				continue // first plugin wins
			}
			reg.Entries[key] = KeybindEntry{
				Combo:  combo,
				Action: action,
				Source: KeybindSource{PluginID: pluginID},
			}
		}
	}

	// 2. User TOML overrides (always win)
	userOverrides := loadUserKeybindOverrides(overridesTomlPath)
	for comboStr, action := range userOverrides {
		combo, ok := parseCombo(comboStr)
		if !ok {
			continue
		}
		key := comboKey(combo)
		if action == "" {
			delete(reg.Entries, key)
		} else {
			reg.Entries[key] = KeybindEntry{
				Combo:  combo,
				Action: action,
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

// --- Plugin state ---

type PluginState struct {
	KeybindsByPlugin  map[string]map[string]string
	OverridesTomlPath string
	Registry          InternalRegistry
	RemappingCombo    string // empty = not remapping
	KeysError string // error message shown on next Keys tab render, then cleared
	// Key names: physical key name → keycode (loaded from data/key_names_macos.json)
	KeyNamesMerged   map[string]uint16
	// Layout: cached from GET /v1/native/keyboard-layout at startup
	LayoutName       string            // e.g. "U.S."
	LayoutMappings   map[string]string // keycode (as string) → character
	LayoutCharacters map[string]string // physical key name → character (joined)
}

func newPluginState() *PluginState {
	return &PluginState{
		KeybindsByPlugin: make(map[string]map[string]string),
		Registry:         newRegistry(),
	}
}

func (ps *PluginState) rebuild() RegistrySnapshot {
	ps.Registry = buildRegistry(ps.KeybindsByPlugin, ps.OverridesTomlPath)
	return ps.Registry.toSnapshot()
}

// --- Request types ---

type BuildRegistryRequest struct {
	OverridesTomlPath string `json:"overrides_toml_path"`
}

type RenderSettingsRequest struct {
	TabKey string `json:"tab_key"`
	Search string `json:"search"`
}

type StartRemapRequest struct {
	Combo string `json:"combo"`
}

type RemapRequest struct {
	OldCombo string `json:"old_combo"`
	NewCombo string `json:"new_combo"`
	IsHold   bool   `json:"is_hold"`
}

// RemapKeydownRequest accepts raw DOM key event properties + remap context.
type RemapKeydownRequest struct {
	DOMKeyEvent
	OldCombo string `json:"old_combo"`
	IsHold   bool   `json:"is_hold"`
}

type ResetRequest struct {
	ComboKey string `json:"combo_key"`
	IsHold   bool   `json:"is_hold"`
}

type OkResponse struct {
	OK bool `json:"ok"`
}

// ControlDirectives tells the actuator's generic proxy to perform platform
// side effects (send control signals, trigger store rebuilds).
type ControlDirectives struct {
	Signals       []string `json:"signals,omitempty"`
	RebuildStores []string `json:"rebuild_stores,omitempty"`
}

// OkWithControl is an OkResponse with optional _control directives.
type OkWithControl struct {
	OK      bool               `json:"ok"`
	Control *ControlDirectives `json:"_control,omitempty"`
}

// SnapshotWithControl is a RegistrySnapshot with optional _control directives.
type SnapshotWithControl struct {
	Entries       []RegistryEntry    `json:"entries"`
	ListenUp      []string           `json:"listen_up"`
	Control       *ControlDirectives `json:"_control,omitempty"`
}

// --- Settings rendering ---

var corePluginIDs = map[string]bool{"voice": true, "keyboard": true, "wm": true}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func sourceGroupName(src KeybindSource) string {
	if src.IsUser {
		return "Custom"
	}
	name := capitalize(src.PluginID)
	if corePluginIDs[src.PluginID] {
		return name + " (Core)"
	}
	return name
}

func sourceBadgeLabel(src KeybindSource) string {
	if src.IsUser {
		return "Custom"
	}
	return capitalize(src.PluginID)
}

func humanizeAction(action string) string {
	parts := strings.Fields(action)
	base := action
	if len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	base = strings.TrimSuffix(base, "-start")
	base = strings.TrimSuffix(base, "-stop")
	label := strings.ReplaceAll(base, "-", " ")
	return capitalize(label)
}

func findActionForCombo(reg *InternalRegistry, comboStr string) string {
	combo, ok := parseCombo(comboStr)
	if !ok {
		return ""
	}
	if e, found := reg.resolve(combo); found {
		return e.Action
	}
	return ""
}

// --- Handlers ---

var (
	mu    sync.Mutex
	state = newPluginState()
)

var plugin *shared.Plugin

// --- on_action: input simulation via native API ---
//
// Each input.* action gets its own typed handler. The SDK demuxes by
// req.Action via plugin.HandleAction(...) — see main().

func logErr(action string, err error) {
	if err != nil {
		shared.Logf("keyboard", "%s: %v", action, err)
	}
}

// Param structs (TypeParams, KeyByNameParams, …) live in actions_gen.go,
// generated from plugin.json's action_types block. Edit that and re-run
// `just gen-plugins` — do not hand-declare these structs here.

// buttonOrLeft returns the resolved button name, defaulting to "left"
// when the pointer is nil or empty.
func buttonOrLeft(b *ClickButton) string {
	if b == nil || *b == "" {
		return "left"
	}
	return string(*b)
}

func handleInputType(req *shared.OnActionRequest) (any, error) {
	var p TypeParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if p.Text == "" {
		return nil, nil
	}
	logErr("input.type", plugin.Call("input.type_text", map[string]any{"text": p.Text}, nil))
	return nil, nil
}

func handleInputKeyByName(req *shared.OnActionRequest) (any, error) {
	var p KeyByNameParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	// "text" strategy: paste text equivalent instead of key event (when no modifiers)
	if p.Strategy != nil && *p.Strategy == KeyByNameStrategyText && len(p.Modifiers) == 0 {
		if textEquiv := keyTextEquivalent(p.Name); textEquiv != "" {
			logErr("input.key_by_name", plugin.Call("input.type_text", map[string]any{"text": textEquiv}, nil))
			return nil, nil
		}
	}
	params := map[string]any{"name": p.Name}
	if len(p.Modifiers) > 0 {
		params["modifiers"] = p.Modifiers
	}
	logErr("input.key_by_name", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputKey(req *shared.OnActionRequest) (any, error) {
	var p KeyParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	logErr("input.key", plugin.Call("input.press_key", map[string]any{"code": p.Code}, nil))
	return nil, nil
}

func handleInputShortcutByName(req *shared.OnActionRequest) (any, error) {
	var p ShortcutByNameParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"name": p.Name}
	if len(p.Modifiers) > 0 {
		params["modifiers"] = p.Modifiers
	}
	logErr("input.shortcut_by_name", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputShortcut(req *shared.OnActionRequest) (any, error) {
	var p ShortcutParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"code": p.Code}
	if len(p.Modifiers) > 0 {
		params["modifiers"] = p.Modifiers
	}
	logErr("input.shortcut", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputRawKey(req *shared.OnActionRequest) (any, error) {
	var p RawKeyParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	direction := "click"
	switch {
	case p.Direction != nil:
		direction = string(*p.Direction)
	case p.Down != nil && *p.Down:
		direction = "press"
	case p.Down != nil:
		direction = "release"
	}
	logErr("input.raw_key", plugin.Call("input.raw_key", map[string]any{"code": p.Code, "direction": direction}, nil))
	return nil, nil
}

func handleInputClick(req *shared.OnActionRequest) (any, error) {
	var p ClickParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	logErr("input.click", plugin.Call("input.click", map[string]any{"button": buttonOrLeft(p.Button)}, nil))
	return nil, nil
}

func handleInputScroll(req *shared.OnActionRequest) (any, error) {
	var p ScrollParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"direction": string(p.Direction)}
	if p.Amount != nil {
		params["amount"] = *p.Amount
	}
	logErr("input.scroll", plugin.Call("input.scroll", params, nil))
	return nil, nil
}

func handleInputMove(req *shared.OnActionRequest) (any, error) {
	var p MoveParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	logErr("input.move", plugin.Call("native.warp_cursor", map[string]any{"x": p.X, "y": p.Y}, nil))
	return nil, nil
}

func handleInputMouseDown(req *shared.OnActionRequest) (any, error) {
	var p MouseDownParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_down", plugin.Call("input.mouse_button", map[string]any{"button": button, "direction": "press"}, nil))
	return nil, nil
}

func handleInputMouseUp(req *shared.OnActionRequest) (any, error) {
	var p MouseUpParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_up", plugin.Call("input.mouse_button", map[string]any{"button": button, "direction": "release"}, nil))
	return nil, nil
}

func handleInputClipboard(req *shared.OnActionRequest) (any, error) {
	var p ClipboardParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"action": string(p.Action)}
	if p.Text != nil && *p.Text != "" {
		params["text"] = *p.Text
	}
	logErr("input.clipboard", plugin.Call("input.clipboard_action", params, nil))
	return nil, nil
}

// keyTextEquivalent returns the text equivalent of a key name for the "text" strategy.
func keyTextEquivalent(name string) string {
	switch strings.ToLower(name) {
	case "return", "enter":
		return "\n"
	case "tab":
		return "\t"
	case "space":
		return " "
	default:
		return ""
	}
}

func handleBuildRegistry(req *BuildRegistryRequest) (any, error) {
	// Read keybinds from the shared store via RPC
	var storeResp struct {
		Data map[string]map[string]string `json:"data"`
	}
	keybindsByPlugin := make(map[string]map[string]string)
	if err := plugin.Call("collection.get", map[string]string{"name": "keybinds"}, &storeResp); err != nil {
		shared.Logf("keyboard", "failed to read store: %v", err)
	} else {
		keybindsByPlugin = storeResp.Data
	}

	mu.Lock()
	defer mu.Unlock()
	state.KeybindsByPlugin = keybindsByPlugin
	state.OverridesTomlPath = req.OverridesTomlPath
	snapshot := state.rebuild()

	return snapshot, nil
}

func handleRenderSettings(req *RenderSettingsRequest) (any, error) {
	var html string
	search := strings.ToLower(req.Search)

	switch req.TabKey {
	case "keys":
		html = renderKeysSettings(search)
	default:
		mu.Lock()
		html = renderSettings(state, search)
		mu.Unlock()
	}

	return shared.RenderSettingsResponse{HTML: html}, nil
}

func handleStartRemap(req *StartRemapRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = req.Combo

	return OkWithControl{
		OK: true,
		Control: &ControlDirectives{
			Signals: []string{"keybind:pause"},
		},
	}, nil
}

func handleRemap(req *RemapRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	result := applyRemap(req.OldCombo, req.NewCombo, req.IsHold)
	return result, nil
}

// applyRemap performs the core remap logic. Caller must hold mu.Lock().
func applyRemap(oldCombo, newCombo string, isHold bool) SnapshotWithControl {
	overrides := loadUserKeybindOverrides(state.OverridesTomlPath)

	if isHold {
		downAction := findActionForCombo(&state.Registry, oldCombo+" DOWN")
		upAction := findActionForCombo(&state.Registry, oldCombo+" UP")
		if downAction != "" {
			overrides[newCombo+" DOWN"] = downAction
		}
		if upAction != "" {
			overrides[newCombo+" UP"] = upAction
		}
		if oldCombo != newCombo {
			overrides[oldCombo+" DOWN"] = ""
			overrides[oldCombo+" UP"] = ""
		}
	} else {
		action := findActionForCombo(&state.Registry, oldCombo)
		if action != "" {
			overrides[newCombo] = action
		}
		if oldCombo != newCombo {
			overrides[oldCombo] = ""
		}
	}

	saveUserKeybindOverrides(overrides, state.OverridesTomlPath)
	state.RemappingCombo = ""
	snapshot := state.rebuild()

	return SnapshotWithControl{
		Entries:  snapshot.Entries,
		ListenUp: snapshot.ListenUp,
		Control: &ControlDirectives{
			Signals:       []string{"keybind:resume"},
			RebuildStores: []string{"keybinds"},
		},
	}
}

func handleRemapKeydown(req *RemapKeydownRequest) (any, error) {
	parsed := parseDOMKeyEvent(req.DOMKeyEvent)

	// Escape → cancel remap
	if parsed.IsEscape {
		mu.Lock()
		defer mu.Unlock()
		state.RemappingCombo = ""
		return OkWithControl{
			OK:      true,
			Control: &ControlDirectives{Signals: []string{"keybind:resume"}},
		}, nil
	}

	// Bare modifier or unknown key → no-op
	if parsed.IsBareModifier {
		return OkResponse{OK: true}, nil
	}

	// No modifiers → reject
	if !parsed.HasModifiers {
		mu.Lock()
		state.KeysError = "Remap requires at least one modifier key."
		mu.Unlock()
		return OkResponse{OK: false}, nil
	}

	// Valid combo → apply remap
	mu.Lock()
	defer mu.Unlock()
	result := applyRemap(req.OldCombo, parsed.Combo, req.IsHold)
	return result, nil
}

func handleCancelRemap(_ *struct{}) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = ""

	return OkWithControl{
		OK: true,
		Control: &ControlDirectives{
			Signals: []string{"keybind:resume"},
		},
	}, nil
}

func handleReset(req *ResetRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	overrides := loadUserKeybindOverrides(state.OverridesTomlPath)

	var action string
	if req.IsHold {
		action = findActionForCombo(&state.Registry, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" UP")
	} else {
		action = findActionForCombo(&state.Registry, req.ComboKey)
		delete(overrides, req.ComboKey)
	}

	if action != "" {
		for k, v := range overrides {
			if v != "" {
				continue
			}
			for _, pluginBinds := range state.KeybindsByPlugin {
				if pluginAction, ok := pluginBinds[k]; ok && pluginAction == action {
					delete(overrides, k)
				}
			}
		}
	}

	saveUserKeybindOverrides(overrides, state.OverridesTomlPath)
	snapshot := state.rebuild()

	return SnapshotWithControl{
		Entries:  snapshot.Entries,
		ListenUp: snapshot.ListenUp,
		Control: &ControlDirectives{
			RebuildStores: []string{"keybinds"},
		},
	}, nil
}

func handleResetAll(_ *struct{}) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	removeOverridesFile(state.OverridesTomlPath)
	snapshot := state.rebuild()

	return SnapshotWithControl{
		Entries:  snapshot.Entries,
		ListenUp: snapshot.ListenUp,
		Control: &ControlDirectives{
			RebuildStores: []string{"keybinds"},
		},
	}, nil
}

func handleStartCapture(_ *struct{}) (any, error) {
	return OkWithControl{
		OK:      true,
		Control: &ControlDirectives{Signals: []string{"keybind:pause"}},
	}, nil
}

func handleStopCapture(_ *struct{}) (any, error) {
	return OkWithControl{
		OK:      true,
		Control: &ControlDirectives{Signals: []string{"keybind:resume"}},
	}, nil
}

// loadAndPushKeycodes loads key_names_macos.json from the plugin data dir,
// stores in plugin state, and pushes to the keycodes store.
// User overrides are handled by the platform collection override system.
func loadAndPushKeycodes(p *shared.Plugin) {
	pluginDir := os.Getenv("BRANCHKIT_PLUGIN_DIR")
	if pluginDir == "" {
		pluginDir = "."
	}

	dataPath := filepath.Join(pluginDir, "data", "key_names_macos.json")
	data, err := os.ReadFile(dataPath)
	if err != nil {
		shared.Logf("keyboard", "Failed to read %s: %v", dataPath, err)
		return
	}
	var keycodes map[string]uint16
	if err := json.Unmarshal(data, &keycodes); err != nil {
		shared.Logf("keyboard", "Failed to parse %s: %v", dataPath, err)
		return
	}

	mu.Lock()
	state.KeyNamesMerged = keycodes
	mu.Unlock()

	// Push as array of objects matching entry_schema: {name, code}
	type keycodeEntry struct {
		Name string `json:"name"`
		Code uint16 `json:"code"`
	}
	entries := make([]keycodeEntry, 0, len(keycodes))
	for name, code := range keycodes {
		entries = append(entries, keycodeEntry{Name: name, Code: code})
	}
	body := struct {
		Name string         `json:"name"`
		Data []keycodeEntry `json:"data"`
	}{Name: "keycodes", Data: entries}
	if err := p.Call("collection.push", body, nil); err != nil {
		shared.Logf("keyboard", "Failed to push keycodes store: %v", err)
		return
	}

	// Set the platform key name cache directly
	namesBody := struct {
		Names map[string]uint16 `json:"names"`
	}{Names: keycodes}
	if err := p.Call("key_names.set", namesBody, nil); err != nil {
		shared.Logf("keyboard", "Failed to set key_names cache: %v", err)
	}

	shared.Logf("keyboard", "Pushed %d keycodes to store", len(keycodes))
}

// refreshKeycodesFromCollection re-reads the keycodes collection (with overrides applied)
// and updates the local state + platform key_names cache.
func refreshKeycodesFromCollection() {
	var resp struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := plugin.Call("collection.get", map[string]string{"name": "keycodes"}, &resp); err != nil {
		shared.Logf("keyboard", "failed to re-read keycodes collection: %v", err)
		return
	}
	if resp.Entries == nil {
		return
	}

	merged := make(map[string]uint16, len(resp.Entries))
	for name, raw := range resp.Entries {
		var v uint16
		// value may be a number or a string number
		if err := json.Unmarshal(raw, &v); err != nil {
			var s string
			if err2 := json.Unmarshal(raw, &s); err2 == nil {
				var n int
				if _, err3 := fmt.Sscanf(s, "%d", &n); err3 == nil {
					v = uint16(n)
				} else {
					shared.Logf("keyboard", "skipping keycode entry %q: unparseable value %s", name, string(raw))
					continue
				}
			} else {
				shared.Logf("keyboard", "skipping keycode entry %q: unexpected value type %s", name, string(raw))
				continue
			}
		}
		merged[name] = v
	}

	mu.Lock()
	state.KeyNamesMerged = merged
	mu.Unlock()

	// Update platform key_names cache
	namesBody := struct {
		Names map[string]uint16 `json:"names"`
	}{Names: merged}
	if err := plugin.Call("key_names.set", namesBody, nil); err != nil {
		shared.Logf("keyboard", "failed to update key_names cache: %v", err)
	}
	shared.Logf("keyboard", "refreshed %d keycodes from collection update", len(merged))
}

// buildLayoutCharacters joins keycodes with layout mappings to produce
// a physicalName → character map. Iterates keycodes directly so aliases
// (multiple names for the same keycode, e.g. "backslash" and "\") all
// get their layout character.
func buildLayoutCharacters(keyNames map[string]uint16, layoutMappings map[string]string) map[string]string {
	chars := make(map[string]string, len(keyNames))
	for name, kc := range keyNames {
		kcStr := fmt.Sprintf("%d", kc)
		if ch, ok := layoutMappings[kcStr]; ok {
			chars[name] = ch
		}
	}
	return chars
}

// loadAndPushLayoutCharacters fetches the keyboard layout from the actuator,
// joins with keycodes, caches locally, and pushes the layout_characters store.
func loadAndPushLayoutCharacters(p *shared.Plugin) {
	type layoutResp struct {
		LayoutID   string            `json:"layout_id"`
		LayoutName string            `json:"layout_name"`
		Mappings   map[string]string `json:"mappings"`
	}
	var layout layoutResp
	if err := p.Call("native.keyboard_layout", nil, &layout); err != nil {
		shared.Logf("keyboard", "Failed to fetch keyboard layout: %v", err)
		return
	}

	mu.Lock()
	merged := state.KeyNamesMerged
	mu.Unlock()

	chars := buildLayoutCharacters(merged, layout.Mappings)

	mu.Lock()
	state.LayoutName = layout.LayoutName
	state.LayoutMappings = layout.Mappings
	state.LayoutCharacters = chars
	mu.Unlock()

	// Push to layout_characters store via RPC
	body := struct {
		Name string            `json:"name"`
		Data map[string]string `json:"data"`
	}{Name: "layout_characters", Data: chars}
	if err := p.Call("collection.push", body, nil); err != nil {
		shared.Logf("keyboard", "Failed to push layout_characters store: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d layout characters to store (layout: %s)",
		len(chars), layout.LayoutID)
}

// loadAndPushKeys loads spoken key names from data/keys.json, enriches with
// layout-specific character entries, and pushes to the "keys" collection.
func loadAndPushKeys(p *shared.Plugin) {
	pluginDir := os.Getenv("BRANCHKIT_PLUGIN_DIR")
	if pluginDir == "" {
		pluginDir = "."
	}

	data, err := os.ReadFile(filepath.Join(pluginDir, "data", "keys.json"))
	if err != nil {
		shared.Logf("keyboard", "Failed to read data/keys.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		shared.Logf("keyboard", "Failed to parse data/keys.json: %v", err)
		return
	}

	// Enrich with layout characters — add single-letter character aliases
	// so users can say the character name on non-US layouts.
	mu.Lock()
	chars := state.LayoutCharacters
	mu.Unlock()
	added := 0
	for physicalName, ch := range chars {
		lower := strings.ToLower(ch)
		if len(lower) == 1 && lower[0] >= 'a' && lower[0] <= 'z' {
			entries[lower] = physicalName
			added++
		}
	}
	if added > 0 {
		shared.Logf("keyboard", "Added %d layout character entries to keys list", added)
	}

	// Push as array of objects matching entry_schema: {spoken, key}
	type keyEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	arr := make([]keyEntry, 0, len(entries))
	for spoken, key := range entries {
		arr = append(arr, keyEntry{Spoken: spoken, Key: key})
	}
	body := struct {
		Name string     `json:"name"`
		Data []keyEntry `json:"data"`
	}{Name: "keys", Data: arr}
	if err := p.Call("collection.push", body, nil); err != nil {
		shared.Logf("keyboard", "Failed to push keys collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to keys collection", len(arr))
}

// loadAndPushModifiers loads spoken modifier names from data/modifiers.json
// and pushes to the "modifiers" collection.
func loadAndPushModifiers(p *shared.Plugin) {
	pluginDir := os.Getenv("BRANCHKIT_PLUGIN_DIR")
	if pluginDir == "" {
		pluginDir = "."
	}

	data, err := os.ReadFile(filepath.Join(pluginDir, "data", "modifiers.json"))
	if err != nil {
		shared.Logf("keyboard", "Failed to read data/modifiers.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		shared.Logf("keyboard", "Failed to parse data/modifiers.json: %v", err)
		return
	}

	// Push as array of objects matching entry_schema: {spoken, key}
	type modEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	arr := make([]modEntry, 0, len(entries))
	for spoken, key := range entries {
		arr = append(arr, modEntry{Spoken: spoken, Key: key})
	}
	body := struct {
		Name string     `json:"name"`
		Data []modEntry `json:"data"`
	}{Name: "modifiers", Data: arr}
	if err := p.Call("collection.push", body, nil); err != nil {
		shared.Logf("keyboard", "Failed to push modifiers collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to modifiers collection", len(arr))
}

func main() {
	plugin = shared.NewPlugin()

	// Push initial data to actuator stores
	loadAndPushKeycodes(plugin)
	loadAndPushLayoutCharacters(plugin)
	loadAndPushKeys(plugin)      // depends on layout_characters for enrichment
	loadAndPushModifiers(plugin)

	// Initial keybind registration — read store, build snapshot, register with platform
	{
		var storeResp struct {
			Data map[string]map[string]string `json:"data"`
		}
		if err := plugin.Call("collection.get", map[string]string{"name": "keybinds"}, &storeResp); err != nil {
			shared.Logf("keyboard", "failed to read keybinds store: %v", err)
		} else {
			mu.Lock()
			state.KeybindsByPlugin = storeResp.Data
			state.OverridesTomlPath = filepath.Join(os.Getenv("BRANCHKIT_APP_SUPPORT"), "keybinds.toml")
			snapshot := state.rebuild()
			mu.Unlock()

			regBody := struct {
				Snapshot any `json:"snapshot"`
			}{Snapshot: snapshot}
			if err := plugin.Call("keybinds.register", regBody, nil); err != nil {
				shared.Logf("keyboard", "keybinds.register failed: %v", err)
			} else {
				shared.Logf("keyboard", "Initial keybind registration complete")
			}
		}
	}

	// Subscribe to events (actuator→plugin notifications)
	plugin.On("_platform.collection.updated", func(params json.RawMessage) {
		var payload struct {
			Store string `json:"store"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		switch payload.Store {
		case "keycodes":
			refreshKeycodesFromCollection()
			return
		case "keybinds":
			// handled below
		default:
			return
		}
		// Re-fetch keybinds from actuator
		var storeResp struct {
			Data map[string]map[string]string `json:"data"`
		}
		if err := plugin.Call("collection.get", map[string]string{"name": "keybinds"}, &storeResp); err != nil {
			shared.Logf("keyboard", "store update: failed to read keybinds: %v", err)
			return
		}
		mu.Lock()
		state.KeybindsByPlugin = storeResp.Data
		snapshot := state.rebuild()
		mu.Unlock()

		// Register keybinds with the platform (replaces content_type side effect)
		regBody := struct {
			Snapshot any `json:"snapshot"`
		}{Snapshot: snapshot}
		if err := plugin.Call("keybinds.register", regBody, nil); err != nil {
			shared.Logf("keyboard", "keybinds.register failed: %v", err)
		}
		shared.Logf("keyboard", "rebuilt keybinds from store update")
	})

	plugin.On("_platform.keyboard.layout_changed", func(params json.RawMessage) {
		shared.Logf("keyboard", "layout changed — re-pushing layout_characters and keys")
		loadAndPushLayoutCharacters(plugin)
		loadAndPushKeys(plugin) // re-enrich with new layout characters
	})

	// Register handlers (actuator→plugin requests)
	shared.HandleTyped(plugin, "build_registry", handleBuildRegistry)
	shared.HandleTyped(plugin, "render_settings", handleRenderSettings)
	shared.HandleTyped(plugin, "start_remap", handleStartRemap)
	shared.HandleTyped(plugin, "remap", handleRemap)
	shared.HandleTyped(plugin, "cancel_remap", handleCancelRemap)
	shared.HandleTyped(plugin, "reset", handleReset)
	shared.HandleTyped(plugin, "reset_all", handleResetAll)
	shared.HandleTyped(plugin, "start_capture", handleStartCapture)
	shared.HandleTyped(plugin, "stop_capture", handleStopCapture)
	shared.HandleTyped(plugin, "parse_key_event", handleParseKeyEvent)
	shared.HandleTyped(plugin, "remap_keydown", handleRemapKeydown)
	// Per-action handlers (replaces the old single on_action switch).
	plugin.HandleAction("input.type", handleInputType)
	plugin.HandleAction("input.key_by_name", handleInputKeyByName)
	plugin.HandleAction("input.key", handleInputKey)
	plugin.HandleAction("input.shortcut_by_name", handleInputShortcutByName)
	plugin.HandleAction("input.shortcut", handleInputShortcut)
	plugin.HandleAction("input.raw_key", handleInputRawKey)
	plugin.HandleAction("input.click", handleInputClick)
	plugin.HandleAction("input.scroll", handleInputScroll)
	plugin.HandleAction("input.move", handleInputMove)
	plugin.HandleAction("input.mouse_down", handleInputMouseDown)
	plugin.HandleAction("input.mouse_up", handleInputMouseUp)
	plugin.HandleAction("input.clipboard", handleInputClipboard)

	// Run the message loop (blocks until stdin closes or SIGTERM)
	plugin.Run()
}
