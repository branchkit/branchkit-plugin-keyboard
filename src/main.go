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
	KeybindsByPlugin  map[string]map[string]string
	Registry          InternalRegistry
	RemappingCombo    string // empty = not remapping
	KeysError         string // error message shown on next Keys tab render, then cleared
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

// --- Globals ---

var (
	mu    sync.Mutex
	state = newPluginState()
)

var plugin *shared.Plugin

// --- RPC handlers ---

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
	saveUserKeybindOverrides(nil)
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
	entries := make([]keycodeEntry, 0, len(keycodes))
	for name, code := range keycodes {
		entries = append(entries, keycodeEntry{Name: name, Code: code})
	}
	if err := toolkit.PushCollection(p, "keycodes", entries); err != nil {
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
	if err := toolkit.PushCollection(p, "layout_characters", chars); err != nil {
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

	// Push as array of objects matching entry_schema: {spoken, key}
	type keyEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	arr := make([]keyEntry, 0, len(entries))
	for spoken, key := range entries {
		arr = append(arr, keyEntry{Spoken: spoken, Key: key})
	}
	if err := toolkit.PushCollection(p, "keys", arr); err != nil {
		shared.Logf("keyboard", "Failed to push keys collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to keys collection", len(arr))
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

	// Push as array of objects matching entry_schema: {spoken, key}
	type modEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	arr := make([]modEntry, 0, len(entries))
	for spoken, key := range entries {
		arr = append(arr, modEntry{Spoken: spoken, Key: key})
	}
	if err := toolkit.PushCollection(p, "modifiers", arr); err != nil {
		shared.Logf("keyboard", "Failed to push modifiers collection: %v", err)
		return
	}
	shared.Logf("keyboard", "Pushed %d entries to modifiers collection", len(arr))
}

// --- Startup ---

func main() {
	plugin = shared.NewPlugin()

	// Load system key repeat settings for hold-to-repeat support
	repeatCfg = loadRepeatConfig(plugin)

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
