package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"branchkit.local/shared"
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
	KeysError         string // error message shown on next Keys tab render, then cleared
	EditingKeyName    string // key name being edited (empty = not editing)
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

var platform *shared.PlatformClient

func hookBuildRegistry(w http.ResponseWriter, r *http.Request) {
	var req BuildRegistryRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Read keybinds from the shared store (already excludes disabled plugins)
	keybindsByPlugin, err := platform.GetKeybindsStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] failed to read store: %v\n", err)
		keybindsByPlugin = make(map[string]map[string]string)
	}

	mu.Lock()
	defer mu.Unlock()
	state.KeybindsByPlugin = keybindsByPlugin
	state.OverridesTomlPath = req.OverridesTomlPath
	snapshot := state.rebuild()

	shared.WriteJSON(w, snapshot)
}

func hookRenderSettings(w http.ResponseWriter, r *http.Request) {
	var req RenderSettingsRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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

	shared.WriteJSON(w, shared.SettingsResponse{HTML: html})
}

func hookStartRemap(w http.ResponseWriter, r *http.Request) {
	var req StartRemapRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = req.Combo

	shared.WriteJSON(w, OkWithControl{
		OK: true,
		Control: &ControlDirectives{
			Signals: []string{"keybind:pause"},
		},
	})
}

func hookRemap(w http.ResponseWriter, r *http.Request) {
	var req RemapRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	result := applyRemap(req.OldCombo, req.NewCombo, req.IsHold)
	shared.WriteJSON(w, result)
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

func hookRemapKeydown(w http.ResponseWriter, r *http.Request) {
	var req RemapKeydownRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	parsed := parseDOMKeyEvent(req.DOMKeyEvent)

	// Escape → cancel remap
	if parsed.IsEscape {
		mu.Lock()
		defer mu.Unlock()
		state.RemappingCombo = ""
		shared.WriteJSON(w, OkWithControl{
			OK:      true,
			Control: &ControlDirectives{Signals: []string{"keybind:resume"}},
		})
		return
	}

	// Bare modifier or unknown key → no-op
	if parsed.IsBareModifier {
		shared.WriteJSON(w, OkResponse{OK: true})
		return
	}

	// No modifiers → reject
	if !parsed.HasModifiers {
		mu.Lock()
		state.KeysError = "Remap requires at least one modifier key."
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	// Valid combo → apply remap
	mu.Lock()
	defer mu.Unlock()
	result := applyRemap(req.OldCombo, parsed.Combo, req.IsHold)
	shared.WriteJSON(w, result)
}

func hookCancelRemap(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	state.RemappingCombo = ""

	shared.WriteJSON(w, OkWithControl{
		OK: true,
		Control: &ControlDirectives{
			Signals: []string{"keybind:resume"},
		},
	})
}

func hookReset(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	overrides := loadUserKeybindOverrides(state.OverridesTomlPath)

	// Find the action for this combo so we can clean up related overrides
	var action string
	if req.IsHold {
		action = findActionForCombo(&state.Registry, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" DOWN")
		delete(overrides, req.ComboKey+" UP")
	} else {
		action = findActionForCombo(&state.Registry, req.ComboKey)
		delete(overrides, req.ComboKey)
	}

	// Remove empty-string overrides that suppress the original default for this action.
	// When a keybind is remapped (e.g. opt+b → opt+v), the old combo gets "" to suppress it.
	// Resetting should remove those suppressions so the default comes back.
	if action != "" {
		for k, v := range overrides {
			if v != "" {
				continue
			}
			// This is a suppression entry — check if the suppressed combo's default
			// action (from plugin manifests, pre-override) matches what we're resetting
			for _, pluginBinds := range state.KeybindsByPlugin {
				if pluginAction, ok := pluginBinds[k]; ok && pluginAction == action {
					delete(overrides, k)
				}
			}
		}
	}

	saveUserKeybindOverrides(overrides, state.OverridesTomlPath)
	snapshot := state.rebuild()

	shared.WriteJSON(w, SnapshotWithControl{
		Entries:  snapshot.Entries,
		ListenUp: snapshot.ListenUp,
		Control: &ControlDirectives{
			RebuildStores: []string{"keybinds"},
		},
	})
}

func hookResetAll(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	removeOverridesFile(state.OverridesTomlPath)
	snapshot := state.rebuild()

	shared.WriteJSON(w, SnapshotWithControl{
		Entries:  snapshot.Entries,
		ListenUp: snapshot.ListenUp,
		Control: &ControlDirectives{
			RebuildStores: []string{"keybinds"},
		},
	})
}

type OnStoreUpdatedRequest struct {
	Store string `json:"store"`
}

func hookOnStoreUpdated(w http.ResponseWriter, r *http.Request) {
	var req OnStoreUpdatedRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Store != "keybinds" {
		shared.WriteJSON(w, OkResponse{OK: true})
		return
	}

	// Re-read the keybinds store and rebuild the internal registry
	keybindsByPlugin, err := platform.GetKeybindsStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] on-store-updated: failed to read store: %v\n", err)
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	mu.Lock()
	defer mu.Unlock()
	state.KeybindsByPlugin = keybindsByPlugin
	state.rebuild()

	shared.WriteJSON(w, OkResponse{OK: true})
}

func hookStartCapture(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, OkWithControl{
		OK:      true,
		Control: &ControlDirectives{Signals: []string{"keybind:pause"}},
	})
}

func hookStopCapture(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, OkWithControl{
		OK:      true,
		Control: &ControlDirectives{Signals: []string{"keybind:resume"}},
	})
}

func main() {
	platform = shared.NewPlatformClient()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", shared.HealthHandler())
	mux.HandleFunc("POST /hooks/build-registry", hookBuildRegistry)
	mux.HandleFunc("POST /hooks/render-settings", hookRenderSettings)
	mux.HandleFunc("POST /hooks/start-remap", hookStartRemap)
	mux.HandleFunc("POST /hooks/remap", hookRemap)
	mux.HandleFunc("POST /hooks/cancel-remap", hookCancelRemap)
	mux.HandleFunc("POST /hooks/reset", hookReset)
	mux.HandleFunc("POST /hooks/reset-all", hookResetAll)
	mux.HandleFunc("POST /hooks/on-store-updated", hookOnStoreUpdated)
	mux.HandleFunc("POST /hooks/start-capture", hookStartCapture)
	mux.HandleFunc("POST /hooks/stop-capture", hookStopCapture)
	mux.HandleFunc("POST /hooks/set-key-name", hookSetKeyName)
	mux.HandleFunc("POST /hooks/delete-key-name", hookDeleteKeyName)
	mux.HandleFunc("POST /hooks/start-edit-key", hookStartEditKey)
	mux.HandleFunc("POST /hooks/cancel-edit-key", hookCancelEditKey)
	mux.HandleFunc("POST /hooks/edit-key-keydown", hookEditKeyKeydown)
	mux.HandleFunc("POST /hooks/parse-key-event", hookParseKeyEvent)
	mux.HandleFunc("POST /hooks/remap-keydown", hookRemapKeydown)

	shared.RunPlugin(mux)
}

// Ensure fmt is used (for any debug logging)
var _ = fmt.Sprintf
