package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/branchkit/plugin-sdk-go"
)

// --- Plugin state ---

type PluginState struct {
	KeybindsByPlugin map[string]map[string]Binding
	// Bind-a-command flow: picker contents (non-nil = open), the candidate
	// awaiting its combo, and a one-shot error shown on next render.
	BindPicker     []bindCandidate
	PendingBind    *bindCandidate
	BindError      string
	Registry       InternalRegistry
	RemappingCombo string // empty = not remapping
	KeysError      string // error message shown on next Keys tab render, then cleared
	// Key names: physical key name → keycode, read from the platform's
	// `_platform.key_names` registry (with user overrides applied).
	KeyNamesMerged map[string]uint16
	// Layout: cached from GET /v1/native/keyboard-layout at startup
	LayoutName     string            // e.g. "U.S."
	LayoutMappings map[string]string // keycode (as string) → character
}

func newPluginState() *PluginState {
	return &PluginState{
		KeybindsByPlugin: make(map[string]map[string]Binding),
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

var plugin *branchkit.Plugin

// fetchKeybindsByPlugin reads the keybinds collection from the actuator
// and regroups the per-record shape into a per-plugin map.
//
// Phase 3.3: keybinds migrated from a Keyed-merge contributions map
// (`{plugin_id: {combo: action}}`) to per-record state.put with
// namespaced ids (each record `{id, plugin_id, combo, action}`). This
// helper restores the per-plugin map the rest of the plugin's logic
// (buildRegistry, applyRemap, etc.) was already coded against.
func fetchKeybindsByPlugin() (map[string]map[string]Binding, error) {
	var storeResp struct {
		Data []struct {
			PluginID string          `json:"plugin_id"`
			Combo    string          `json:"combo"`
			Action   string          `json:"action"`
			Params   json.RawMessage `json:"params"`
		} `json:"data"`
	}
	if err := plugin.Call("collection.get", map[string]string{"name": "keybinds"}, &storeResp); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]Binding)
	for _, r := range storeResp.Data {
		if r.PluginID == "" || r.Combo == "" {
			continue
		}
		if out[r.PluginID] == nil {
			out[r.PluginID] = make(map[string]Binding)
		}
		out[r.PluginID][r.Combo] = Binding{Action: r.Action, Params: r.Params}
	}
	return out, nil
}

// --- RPC handlers ---

func handleBuildRegistry(req *BuildRegistryRequest) (any, error) {
	keybindsByPlugin, err := fetchKeybindsByPlugin()
	if err != nil {
		branchkit.Logf("keyboard", "failed to read store: %v", err)
		keybindsByPlugin = make(map[string]map[string]Binding)
	}

	mu.Lock()
	defer mu.Unlock()
	state.KeybindsByPlugin = keybindsByPlugin
	snapshot := state.rebuild()

	return snapshot, nil
}

func handleRenderSettings(req *branchkit.RenderSettingsRequest) (any, error) {
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

	return branchkit.RenderSettingsResponse{HTML: html}, nil
}

func handleStartRemap(req *StartRemapRequest) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = req.Combo

	pauseKeybinds()
	return OkResponse{OK: true}, nil
}

func handleRemap(req *RemapRequest) (any, error) {
	mu.Lock()
	result := applyRemap(req.OldCombo, req.NewCombo, req.IsHold)
	mu.Unlock()
	// Outside the lock: registration is an RPC, and holding mu across a
	// blocking call would stall every other handler on a slow actuator.
	registerKeybinds(result)
	return result, nil
}

// applyRemap performs the core remap logic. Caller must hold mu.Lock().
func applyRemap(oldCombo, newCombo string, isHold bool) RegistrySnapshot {
	overrides := loadUserKeybindOverrides()

	if isHold {
		downAction := findActionForCombo(&state.Registry, oldCombo+" DOWN")
		upAction := findActionForCombo(&state.Registry, oldCombo+" UP")
		if !downAction.IsZero() {
			overrides[newCombo+" DOWN"] = downAction
		}
		if !upAction.IsZero() {
			overrides[newCombo+" UP"] = upAction
		}
		if oldCombo != newCombo {
			overrides[oldCombo+" DOWN"] = Binding{}
			overrides[oldCombo+" UP"] = Binding{}
		}
	} else {
		action := findActionForCombo(&state.Registry, oldCombo)
		if !action.IsZero() {
			overrides[newCombo] = action
		}
		if oldCombo != newCombo {
			overrides[oldCombo] = Binding{}
		}
	}

	saveUserKeybindOverrides(overrides)
	state.RemappingCombo = ""
	snapshot := state.rebuild()

	resumeKeybinds()
	return snapshot
}

func handleRemapKeydown(req *RemapKeydownRequest) (any, error) {
	parsed := parseDOMKeyEvent(req.DOMKeyEvent)

	// Escape → cancel remap
	if parsed.IsEscape {
		mu.Lock()
		defer mu.Unlock()
		state.RemappingCombo = ""
		resumeKeybinds()
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
	result := applyRemap(req.OldCombo, parsed.Combo, req.IsHold)
	mu.Unlock()
	registerKeybinds(result)
	return result, nil
}

func handleCancelRemap(_ *struct{}) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = ""

	resumeKeybinds()
	return OkResponse{OK: true}, nil
}

func handleReset(req *ResetRequest) (any, error) {
	mu.Lock()
	overrides := loadUserKeybindOverrides()

	var action Binding
	if req.IsHold {
		action = findActionForCombo(&state.Registry, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" UP")
	} else {
		action = findActionForCombo(&state.Registry, req.ComboKey)
		delete(overrides, req.ComboKey)
	}

	if !action.IsZero() {
		for k, v := range overrides {
			if !v.IsZero() {
				continue
			}
			for _, pluginBinds := range state.KeybindsByPlugin {
				if pluginBind, ok := pluginBinds[k]; ok && pluginBind.Action == action.Action {
					delete(overrides, k)
				}
			}
		}
	}

	saveUserKeybindOverrides(overrides)
	snapshot := state.rebuild()
	mu.Unlock()
	registerKeybinds(snapshot)

	return snapshot, nil
}

func handleResetAll(_ *struct{}) (any, error) {
	mu.Lock()
	saveUserKeybindOverrides(nil)
	snapshot := state.rebuild()
	mu.Unlock()
	registerKeybinds(snapshot)

	return snapshot, nil
}

// pauseKeybinds / resumeKeybinds hold and release the `suppress_keybinds`
// effect around a key capture.
//
// This replaced raw `ControlSignal("keybind:pause")` — a global lease with no
// owner, no refcount and no expiry, where a crash between pause and resume
// left every hotkey dead.
// As an effect the platform owns the lifetime: per-plugin stack frames, the
// actual pause only on the empty→held transition, resume only when the LAST
// holder releases (so an overlapping voice-editor capture isn't stomped —
// the accepted race of the old boot-time resume is gone), and release in
// `cleanup_terminated_plugin` if this process dies mid-capture, on every
// platform. No boot-time reconcile needed anymore for exactly that reason.
func pauseKeybinds() {
	if plugin == nil {
		return
	}
	out, err := plugin.AssertEffect("suppress_keybinds")
	if err != nil {
		branchkit.Logf("keyboard", "suppress_keybinds assert failed: %v", err)
		return
	}
	if !out.Enforced {
		// Capture still proceeds — worst case a hotkey fires mid-capture,
		// same as the pre-pause world — but say so, loudly enough to find.
		branchkit.Logf("keyboard", "suppress_keybinds not enforced — captured keys may also trigger commands")
	}
}

func resumeKeybinds() {
	if plugin == nil {
		return
	}
	if _, _, err := plugin.RetractEffect("suppress_keybinds"); err != nil {
		branchkit.Logf("keyboard", "suppress_keybinds retract failed: %v", err)
	}
}

// registerKeybinds pushes a rebuilt snapshot to the platform, which forwards
// it to the shell's KeybindCapture as a `keybind:register` control message.
//
// Every path that CHANGES the effective bindings must call this, and until
// 2026-08-14 the override paths didn't: remap and reset saved user overrides
// to `plugin.keyboard.overrides` and rebuilt the LOCAL registry, but the only
// re-registration trigger was the collection.updated subscription for the
// base `keybinds` collection — overrides hit its `default: return`. So a
// remap looked successful in Settings while the shell kept firing the OLD
// combos until the next plugin restart. A test seam (var) so handler tests
// can assert the registration actually happens.
var registerKeybinds = func(snapshot RegistrySnapshot) {
	if plugin == nil {
		return
	}
	regBody := struct {
		Snapshot any `json:"snapshot"`
	}{Snapshot: snapshot}
	if err := plugin.Call("keybinds.register", regBody, nil); err != nil {
		branchkit.Logf("keyboard", "keybinds.register failed: %v", err)
		return
	}
	branchkit.Logf("keyboard", "re-registered keybinds after override change")
}

func handleStartCapture(_ *struct{}) (any, error) {
	pauseKeybinds()
	return OkResponse{OK: true}, nil
}

func handleStopCapture(_ *struct{}) (any, error) {
	resumeKeybinds()
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
	// Actions are exact dotted types ("voice.dictation", "tiling.move_to");
	// the label is the leaf with underscores opened up.
	base := action
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[i+1:]
	}
	label := strings.ReplaceAll(base, "_", " ")
	return capitalize(label)
}

func findActionForCombo(reg *InternalRegistry, comboStr string) Binding {
	combo, ok := parseCombo(comboStr)
	if !ok {
		return Binding{}
	}
	if e, found := reg.resolve(combo); found {
		return Binding{Action: e.Action, Params: e.Params}
	}
	return Binding{}
}

// --- Data loading ---

// keyNamesCollection is the platform's key-name registry. This plugin used to
// introduce and seed it (as `keycodes`), which made the platform's ability to
// resolve a key name depend on this plugin being installed. The platform owns
// and seeds it now; keyboard is one consumer among possible others, and may
// contribute names on top of the baseline.
const keyNamesCollection = "_platform.key_names"

// refreshKeycodesFromCollection reads the platform's key-name registry (with
// overrides applied) into local state, which feeds layout characters, the Keys
// settings tab, and hold-to-repeat's code lookup. Read-only: the platform seeds
// and resolves from the same records, so there is nothing to push back.
func refreshKeycodesFromCollection() {
	var resp struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := plugin.Call("collection.get", map[string]string{"name": keyNamesCollection}, &resp); err != nil {
		branchkit.Logf("keyboard", "failed to read %s: %v", keyNamesCollection, err)
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
					branchkit.Logf("keyboard", "skipping keycode entry %q: unparseable value %s", name, string(raw))
					continue
				}
			} else {
				branchkit.Logf("keyboard", "skipping keycode entry %q: unexpected value type %s", name, string(raw))
				continue
			}
		}
		merged[name] = v
	}

	mu.Lock()
	state.KeyNamesMerged = merged
	mu.Unlock()

	branchkit.Logf("keyboard", "read %d key names from %s", len(merged), keyNamesCollection)
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
func loadAndPushLayoutCharacters(p *branchkit.Plugin) {
	type layoutResp struct {
		LayoutID   string            `json:"layout_id"`
		LayoutName string            `json:"layout_name"`
		Mappings   map[string]string `json:"mappings"`
	}
	var layout layoutResp
	if err := p.Call("native.keyboard_layout", nil, &layout); err != nil {
		branchkit.Logf("keyboard", "Failed to fetch keyboard layout: %v", err)
		return
	}

	mu.Lock()
	merged := state.KeyNamesMerged
	mu.Unlock()

	chars := buildLayoutCharacters(merged, layout.Mappings)

	mu.Lock()
	state.LayoutName = layout.LayoutName
	state.LayoutMappings = layout.Mappings
	mu.Unlock()

	// `layout_characters` is a singleton — one record holds the whole
	// physical-name → layout-character map. Consumers (voice plugin's
	// fetchLayoutCharacters) read the singleton's payload as a flat map.
	if err := p.Put("layout_characters", "singleton", chars); err != nil {
		branchkit.Logf("keyboard", "Failed to push layout_characters store: %v", err)
		return
	}
	branchkit.Logf("keyboard", "Pushed %d layout characters to store (layout: %s)",
		len(chars), layout.LayoutID)
}

// loadAndPushKeys loads spoken key names from data/keys.json, enriches with
// layout-specific character entries, and pushes to the "keys" collection.
func loadAndPushKeys(p *branchkit.Plugin) {
	data, err := os.ReadFile(filepath.Join(branchkit.PluginDir(), "data", "keys.json"))
	if err != nil {
		branchkit.Logf("keyboard", "Failed to read data/keys.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		branchkit.Logf("keyboard", "Failed to parse data/keys.json: %v", err)
		return
	}

	// `keys` declares feeds_matching: as_named_entities with
	// key_field: "spoken" — each record's id is its spoken form.
	type keyEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	records := make([]branchkit.CollectionPutEntry, 0, len(entries))
	for spoken, key := range entries {
		raw, err := json.Marshal(keyEntry{Spoken: spoken, Key: key})
		if err != nil {
			branchkit.Logf("keyboard", "keys: marshal %q: %v", spoken, err)
			return
		}
		records = append(records, branchkit.CollectionPutEntry{ID: spoken, Payload: raw})
	}
	if _, err := p.Replace("keys", records, branchkit.ScopeCollection()); err != nil {
		branchkit.Logf("keyboard", "Failed to push keys collection: %v", err)
		return
	}
	branchkit.Logf("keyboard", "Pushed %d entries to keys collection", len(records))
}

// loadAndPushModifiers loads spoken modifier names from data/modifiers.json
// and pushes to the "modifiers" collection.
func loadAndPushModifiers(p *branchkit.Plugin) {
	data, err := os.ReadFile(filepath.Join(branchkit.PluginDir(), "data", "modifiers.json"))
	if err != nil {
		branchkit.Logf("keyboard", "Failed to read data/modifiers.json: %v", err)
		return
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		branchkit.Logf("keyboard", "Failed to parse data/modifiers.json: %v", err)
		return
	}

	// `modifiers` declares feeds_matching: as_named_entities with
	// key_field: "spoken" — each record's id is its spoken form.
	type modEntry struct {
		Spoken string `json:"spoken"`
		Key    string `json:"key"`
	}
	records := make([]branchkit.CollectionPutEntry, 0, len(entries))
	for spoken, key := range entries {
		raw, err := json.Marshal(modEntry{Spoken: spoken, Key: key})
		if err != nil {
			branchkit.Logf("keyboard", "modifiers: marshal %q: %v", spoken, err)
			return
		}
		records = append(records, branchkit.CollectionPutEntry{ID: spoken, Payload: raw})
	}
	if _, err := p.Replace("modifiers", records, branchkit.ScopeCollection()); err != nil {
		branchkit.Logf("keyboard", "Failed to push modifiers collection: %v", err)
		return
	}
	branchkit.Logf("keyboard", "Pushed %d entries to modifiers collection", len(records))
}

// --- Startup ---

func main() {
	plugin = branchkit.NewPlugin()

	// Load system key repeat settings for hold-to-repeat support
	repeatCfg = loadRepeatConfig(plugin)

	// Key names come FROM the platform now — read them before the loaders
	// below, which enrich against them.
	refreshKeycodesFromCollection()

	// Push initial data to actuator stores
	loadAndPushLayoutCharacters(plugin)
	loadAndPushKeys(plugin) // depends on layout_characters for enrichment
	loadAndPushModifiers(plugin)

	// Initial keybind registration — read store, build snapshot, register with platform
	if kbp, err := fetchKeybindsByPlugin(); err != nil {
		branchkit.Logf("keyboard", "failed to read keybinds store: %v", err)
	} else {
		mu.Lock()
		state.KeybindsByPlugin = kbp
		snapshot := state.rebuild()
		mu.Unlock()

		regBody := struct {
			Snapshot any `json:"snapshot"`
		}{Snapshot: snapshot}
		if err := plugin.Call("keybinds.register", regBody, nil); err != nil {
			branchkit.Logf("keyboard", "keybinds.register failed: %v", err)
		} else {
			branchkit.Logf("keyboard", "Initial keybind registration complete")
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
		case keyNamesCollection:
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
			branchkit.Logf("keyboard", "store update: failed to read keybinds: %v", err)
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
			branchkit.Logf("keyboard", "keybinds.register failed: %v", err)
		}
		branchkit.Logf("keyboard", "rebuilt keybinds from store update")
	})

	plugin.On("_platform.keyboard.layout_changed", func(params json.RawMessage) {
		branchkit.Logf("keyboard", "layout changed — re-pushing layout_characters and keys")
		loadAndPushLayoutCharacters(plugin)
		loadAndPushKeys(plugin) // re-enrich with new layout characters
	})

	// Register handlers (actuator→plugin requests)
	branchkit.HandleTyped(plugin, "build_registry", handleBuildRegistry)
	branchkit.HandleTyped(plugin, "render_settings", handleRenderSettings)
	branchkit.HandleTyped(plugin, "start_remap", handleStartRemap)
	branchkit.HandleTyped(plugin, "remap", handleRemap)
	branchkit.HandleTyped(plugin, "cancel_remap", handleCancelRemap)
	branchkit.HandleTyped(plugin, "reset", handleReset)
	branchkit.HandleTyped(plugin, "reset_all", handleResetAll)
	branchkit.HandleTyped(plugin, "start_capture", handleStartCapture)
	branchkit.HandleTyped(plugin, "stop_capture", handleStopCapture)
	branchkit.HandleTyped(plugin, "parse_key_event", handleParseKeyEvent)
	branchkit.HandleTyped(plugin, "remap_keydown", handleRemapKeydown)
	branchkit.HandleTyped(plugin, "open_bind_picker", handleOpenBindPicker)
	branchkit.HandleTyped(plugin, "close_bind_picker", handleCloseBindPicker)
	branchkit.HandleTyped(plugin, "choose_bind", handleChooseBind)
	branchkit.HandleTyped(plugin, "cancel_bind", handleCancelBind)
	branchkit.HandleTyped(plugin, "bind_keydown", handleBindKeydown)
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
