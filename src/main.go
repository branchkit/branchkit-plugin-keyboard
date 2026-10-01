package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

//go:embed settings.css
var keyboardCSS string

// --- Plugin state ---

type PluginState struct {
	KeysError string // error message shown on next Keys tab render, then cleared
	// Key names: physical key name → keycode, read from the platform's
	// `_platform.key_names` registry (with user overrides applied).
	KeyNamesMerged map[string]uint16
	// Layout: cached from GET /v1/native/keyboard-layout at startup
	LayoutName     string            // e.g. "U.S."
	LayoutMappings map[string]string // keycode (as string) → character
}

func newPluginState() *PluginState {
	return &PluginState{}
}

// --- RPC handlers ---

func (h *Host) renderKeysTab(req *branchkit.RenderSettingsRequest) (string, error) {
	return h.renderKeysSettings(strings.ToLower(req.Search))
}

// warnLegacyOverrides says so when edits saved by this plugin's old Keybinds
// tab are still here. That tab moved into Settings → Keybinds, and only the
// platform writes keybind edits now, so this plugin cannot carry them over:
// the version that could did so on its first start.
func (h *Host) warnLegacyOverrides() {
	rec, err := h.plugin.Get(legacyOverridesCollection, "singleton")
	if err != nil || rec == nil {
		return
	}
	var legacy map[string]json.RawMessage
	if json.Unmarshal(rec.Payload, &legacy) != nil || len(legacy) == 0 {
		return
	}
	branchkit.Logf("keyboard", "%d keybind edit(s) saved by an older version are still in %s and were "+
		"not carried over — re-create them in Settings → Keybinds", len(legacy), legacyOverridesCollection)
}

// legacyOverridesCollection held the user's keybind edits as one whole-map
// singleton until 2026-09-30.
const legacyOverridesCollection = "plugin.keyboard.overrides"

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
func (h *Host) refreshKeycodesFromCollection() {
	resp, err := h.plugin.CollectionGet(branchkit.CollectionGetRequest{Name: keyNamesCollection})
	if err != nil {
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

	h.mu.Lock()
	h.state.KeyNamesMerged = merged
	h.mu.Unlock()

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
func (h *Host) loadAndPushLayoutCharacters(p *branchkit.Plugin) {
	type layoutResp struct {
		LayoutID   string            `json:"layout_id"`
		LayoutName string            `json:"layout_name"`
		Mappings   map[string]string `json:"mappings"`
	}
	layout, err := p.NativeKeyboardLayout()
	if err != nil {
		branchkit.Logf("keyboard", "Failed to fetch keyboard layout: %v", err)
		return
	}

	h.mu.Lock()
	merged := h.state.KeyNamesMerged
	h.mu.Unlock()

	chars := buildLayoutCharacters(merged, layout.Mappings)

	h.mu.Lock()
	h.state.LayoutName = layout.LayoutName
	h.state.LayoutMappings = layout.Mappings
	h.mu.Unlock()

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
	h := newHost(branchkit.NewPlugin())

	// Load system key repeat settings for hold-to-repeat support
	h.repeatCfg = loadRepeatConfig(h.plugin)

	// Key names come FROM the platform now — read them before the loaders
	// below, which enrich against them.
	h.refreshKeycodesFromCollection()

	// Push initial data to actuator stores
	h.loadAndPushLayoutCharacters(h.plugin)
	loadAndPushKeys(h.plugin) // depends on layout_characters for enrichment
	loadAndPushModifiers(h.plugin)

	h.warnLegacyOverrides()

	// Subscribe to events (actuator→plugin notifications)
	h.plugin.On("_platform.collection.updated", func(params json.RawMessage) {
		var payload struct {
			Collection string `json:"collection"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		if payload.Collection == keyNamesCollection {
			h.refreshKeycodesFromCollection()
		}
	})

	h.plugin.On("_platform.keyboard.layout_changed", func(params json.RawMessage) {
		branchkit.Logf("keyboard", "layout changed — re-pushing layout_characters and keys")
		h.loadAndPushLayoutCharacters(h.plugin)
		loadAndPushKeys(h.plugin) // re-enrich with new layout characters
	})

	// Register handlers (actuator→plugin requests)
	h.plugin.SettingsCSS(keyboardCSS)
	h.plugin.SettingsTab("keys", h.renderKeysTab)

	// Per-action handlers (replaces the old single on_action switch).
	HandleType(h.plugin, h.handleInputType)
	HandleKeyByName(h.plugin, h.handleInputKeyByName)
	HandleKey(h.plugin, h.handleInputKey)
	HandleShortcutByName(h.plugin, h.handleInputShortcutByName)
	HandleShortcut(h.plugin, h.handleInputShortcut)
	HandleRawKey(h.plugin, h.handleInputRawKey)
	HandleClick(h.plugin, h.handleInputClick)
	HandleScroll(h.plugin, h.handleInputScroll)
	HandleMove(h.plugin, h.handleInputMove)
	HandleMouseDown(h.plugin, h.handleInputMouseDown)
	HandleMouseUp(h.plugin, h.handleInputMouseUp)
	HandleClipboard(h.plugin, h.handleInputClipboard)

	// Run the message loop (blocks until stdin closes or SIGTERM)
	h.plugin.Run()
}
