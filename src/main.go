package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/branchkit/plugin-sdk-go"
	toolkit "github.com/branchkit/plugin-toolkit-go"
)

// --- Plugin state ---

type PluginState struct {
	KeybindsByPlugin map[string]map[string]string
	Registry         InternalRegistry
	RemappingCombo   string // empty = not remapping
	KeysError        string // error message shown on next Keys tab render, then cleared
	// Key names: physical key name → keycode (loaded from data/key_names_macos.json)
	KeyNamesMerged map[string]uint16
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
	ps.Registry = buildRegistry(ps.KeybindsByPlugin)
	return ps.Registry.toSnapshot()
}

// --- Request types ---

type BuildRegistryRequest struct {
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

// --- Globals ---

var (
	mu    sync.Mutex
	state = newPluginState()
)

var plugin *shared.Plugin

// fetchKeybindsByPlugin reads the keybinds collection from the actuator
// and regroups the per-record shape into a per-plugin map.
//
// Phase 3.3: keybinds migrated from a Keyed-merge contributions map
// (`{plugin_id: {combo: action}}`) to per-record state.put with
// namespaced ids (each record `{id, plugin_id, combo, action}`). This
// helper restores the per-plugin map the rest of the plugin's logic
// (buildRegistry, applyRemap, etc.) was already coded against.
func fetchKeybindsByPlugin() (map[string]map[string]string, error) {
	var storeResp struct {
		Data []struct {
			PluginID string `json:"plugin_id"`
			Combo    string `json:"combo"`
			Action   string `json:"action"`
		} `json:"data"`
	}
	if err := plugin.Call("collection.get", map[string]string{"name": "keybinds"}, &storeResp); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string)
	for _, r := range storeResp.Data {
		if r.PluginID == "" || r.Combo == "" {
			continue
		}
		if out[r.PluginID] == nil {
			out[r.PluginID] = make(map[string]string)
		}
		out[r.PluginID][r.Combo] = r.Action
	}
	return out, nil
}

// --- RPC handlers ---

func handleBuildRegistry(req *BuildRegistryRequest) (any, error) {
	keybindsByPlugin, err := fetchKeybindsByPlugin()
	if err != nil {
		shared.Logf("keyboard", "failed to read store: %v", err)
		keybindsByPlugin = make(map[string]map[string]string)
	}

	mu.Lock()
	defer mu.Unlock()
	state.KeybindsByPlugin = keybindsByPlugin
	snapshot := state.rebuild()

	return snapshot, nil
}

func handleRenderSettings(req *shared.RenderSettingsRequest) (any, error) {
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

	plugin.ControlSignal("keybind:pause")
	return OkResponse{OK: true}, nil
}

func handleRemap(req *RemapRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	result := applyRemap(req.OldCombo, req.NewCombo, req.IsHold)
	return result, nil
}

// applyRemap performs the core remap logic. Caller must hold mu.Lock().
func applyRemap(oldCombo, newCombo string, isHold bool) RegistrySnapshot {
	overrides := loadUserKeybindOverrides()

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

	saveUserKeybindOverrides(overrides)
	state.RemappingCombo = ""
	snapshot := state.rebuild()

	plugin.ControlSignal("keybind:resume")
	return snapshot
}

func handleRemapKeydown(req *RemapKeydownRequest) (any, error) {
	parsed := parseDOMKeyEvent(req.DOMKeyEvent)

	// Escape → cancel remap
	if parsed.IsEscape {
		mu.Lock()
		defer mu.Unlock()
		state.RemappingCombo = ""
		plugin.ControlSignal("keybind:resume")
		return OkResponse{OK: true}, nil
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

	plugin.ControlSignal("keybind:resume")
	return OkResponse{OK: true}, nil
}

func handleReset(req *ResetRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	overrides := loadUserKeybindOverrides()

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

	saveUserKeybindOverrides(overrides)
	snapshot := state.rebuild()

	return snapshot, nil
}

func handleResetAll(_ *struct{}) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	saveUserKeybindOverrides(nil)
	snapshot := state.rebuild()

	return snapshot, nil
}

func handleStartCapture(_ *struct{}) (any, error) {
	plugin.ControlSignal("keybind:pause")
	return OkResponse{OK: true}, nil
}

func handleStopCapture(_ *struct{}) (any, error) {
	plugin.ControlSignal("keybind:resume")
	return OkResponse{OK: true}, nil
}

// --- Settings rendering helpers ---

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

// --- Data loading ---

// loadAndPushKeycodes loads key_names_macos.json from the plugin data dir,
// stores in plugin state, and pushes to the keycodes store.
// User overrides are handled by the platform collection override system.
func loadAndPushKeycodes(p *shared.Plugin) {
	dataPath := filepath.Join(toolkit.PluginDir(), "data", "key_names_macos.json")
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
	// `keycodes` declares feeds_matching: as_named_entities with
	// key_field: "name" — each record's id is its name.
	records := make([]toolkit.Record, 0, len(keycodes))
	for name, code := range keycodes {
		records = append(records, toolkit.Record{
			ID:      name,
			Payload: keycodeEntry{Name: name, Code: code},
		})
	}
	if err := toolkit.ReplaceCollection(p, "keycodes", records); err != nil {
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

	// `layout_characters` is a singleton — one record holds the whole
	// physical-name → layout-character map. Consumers (voice plugin's
	// fetchLayoutCharacters) read the singleton's payload as a flat map.
	if err := p.Put("layout_characters", "singleton", chars); err != nil {
		shared.Logf("keyboard", "Failed to push layout_characters store: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d layout characters to store (layout: %s)",
		len(chars), layout.LayoutID)
}

// loadAndPushKeys loads spoken key names from data/keys.json, enriches with
// layout-specific character entries, and pushes to the "keys" collection.
func loadAndPushKeys(p *shared.Plugin) {
	data, err := os.ReadFile(filepath.Join(toolkit.PluginDir(), "data", "keys.json"))
	if err != nil {
		shared.Logf("keyboard", "Failed to read data/keys.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		shared.Logf("keyboard", "Failed to parse data/keys.json: %v", err)
		return
	}

	// `keys` declares feeds_matching: as_named_entities with
	// key_field: "spoken" — each record's id is its spoken form.
	type keyEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	records := make([]toolkit.Record, 0, len(entries))
	for spoken, key := range entries {
		records = append(records, toolkit.Record{
			ID:      spoken,
			Payload: keyEntry{Spoken: spoken, Key: key},
		})
	}
	if err := toolkit.ReplaceCollection(p, "keys", records); err != nil {
		shared.Logf("keyboard", "Failed to push keys collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to keys collection", len(records))
}

// loadAndPushModifiers loads spoken modifier names from data/modifiers.json
// and pushes to the "modifiers" collection.
func loadAndPushModifiers(p *shared.Plugin) {
	data, err := os.ReadFile(filepath.Join(toolkit.PluginDir(), "data", "modifiers.json"))
	if err != nil {
		shared.Logf("keyboard", "Failed to read data/modifiers.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		shared.Logf("keyboard", "Failed to parse data/modifiers.json: %v", err)
		return
	}

	// `modifiers` declares feeds_matching: as_named_entities with
	// key_field: "spoken" — each record's id is its spoken form.
	type modEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	records := make([]toolkit.Record, 0, len(entries))
	for spoken, key := range entries {
		records = append(records, toolkit.Record{
			ID:      spoken,
			Payload: modEntry{Spoken: spoken, Key: key},
		})
	}
	if err := toolkit.ReplaceCollection(p, "modifiers", records); err != nil {
		shared.Logf("keyboard", "Failed to push modifiers collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to modifiers collection", len(records))
}

// --- Startup ---

func main() {
	plugin = shared.NewPlugin()

	// Load system key repeat settings for hold-to-repeat support
	repeatCfg = loadRepeatConfig(plugin)

	// Push initial data to actuator stores
	loadAndPushKeycodes(plugin)
	loadAndPushLayoutCharacters(plugin)
	loadAndPushKeys(plugin) // depends on layout_characters for enrichment
	loadAndPushModifiers(plugin)

	// Initial keybind registration — read store, build snapshot, register with platform
	if kbp, err := fetchKeybindsByPlugin(); err != nil {
		shared.Logf("keyboard", "failed to read keybinds store: %v", err)
	} else {
		mu.Lock()
		state.KeybindsByPlugin = kbp
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

	// Subscribe to events (actuator→plugin notifications)
	plugin.On("_platform.collection.updated", func(params json.RawMessage) {
		var payload struct {
			Collection string `json:"collection"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		switch payload.Collection {
		case "keycodes":
			refreshKeycodesFromCollection()
			return
		case "keybinds":
			// handled below
		default:
			return
		}
		// Re-fetch keybinds from actuator
		kbp, err := fetchKeybindsByPlugin()
		if err != nil {
			shared.Logf("keyboard", "store update: failed to read keybinds: %v", err)
			return
		}
		mu.Lock()
		state.KeybindsByPlugin = kbp
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
	HandleType(plugin, handleInputType)
	HandleKeyByName(plugin, handleInputKeyByName)
	HandleKey(plugin, handleInputKey)
	HandleShortcutByName(plugin, handleInputShortcutByName)
	HandleShortcut(plugin, handleInputShortcut)
	HandleRawKey(plugin, handleInputRawKey)
	HandleClick(plugin, handleInputClick)
	HandleScroll(plugin, handleInputScroll)
	HandleMove(plugin, handleInputMove)
	HandleMouseDown(plugin, handleInputMouseDown)
	HandleMouseUp(plugin, handleInputMouseUp)
	HandleClipboard(plugin, handleInputClipboard)

	// Run the message loop (blocks until stdin closes or SIGTERM)
	plugin.Run()
}
