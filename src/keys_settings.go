package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"branchkit.local/shared"
)

//go:embed templates/keys.html
var keysTemplateHTML string

var keysTemplate = template.Must(template.New("keys").Parse(keysTemplateHTML))

type keyNameEntry struct {
	Name    string `json:"name"`
	Keycode uint16 `json:"keycode"`
	Source  string `json:"source"`
}

type keyNameView struct {
	Name      string
	NameJSON  template.JS // JSON-escaped name, kept for edit/reset hooks (hidden until key remapping)
	Keycode   uint16
	Character string
}

type keysTemplateData struct {
	Keys           []keyNameView
	Count          int
	Error          string
	EditingKeyName string // name being edited (empty = not editing)
	LayoutName     string // detected OS keyboard layout name
}

// localKeyNames returns key name entries from the plugin's in-memory state.
// No actuator call needed — the keyboard plugin owns this data.
func localKeyNames() []keyNameEntry {
	mu.Lock()
	if state.KeyNamesMerged == nil {
		mu.Unlock()
		return nil
	}
	// Copy under lock to avoid races with setKeyNameOverride/deleteKeyNameOverride
	entries := make([]keyNameEntry, 0, len(state.KeyNamesMerged))
	for name, keycode := range state.KeyNamesMerged {
		source := "default"
		if _, ok := state.KeyNameOverrides[name]; ok {
			source = "user"
		}
		entries = append(entries, keyNameEntry{Name: name, Keycode: keycode, Source: source})
	}
	mu.Unlock()
	return entries
}

// isPrintable returns true if the string contains only visible, printable characters.
// Returns false for control characters, whitespace-only strings, and empty strings.
func isPrintable(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func renderKeysSettings(search string) string {
	keys := localKeyNames()

	// Read layout data from local state (cached at startup)
	mu.Lock()
	layoutMappings := state.LayoutMappings
	layoutName := state.LayoutName
	keysError := state.KeysError
	state.KeysError = ""
	editingKeyName := state.EditingKeyName
	mu.Unlock()

	if layoutMappings == nil {
		layoutMappings = map[string]string{}
	}
	if layoutName == "" {
		layoutName = "Unknown"
	}

	var views []keyNameView
	for _, k := range keys {
		if search != "" && !strings.Contains(strings.ToLower(k.Name), search) {
			continue
		}

		character := layoutMappings[fmt.Sprintf("%d", k.Keycode)]
		if character == "" || !isPrintable(character) {
			character = "–"
		}

		// JSON-encode name for safe interpolation in Datastar expressions
		nameBytes, _ := json.Marshal(k.Name)
		nameJSON := template.JS(string(nameBytes))

		views = append(views, keyNameView{
			Name:      k.Name,
			NameJSON:  nameJSON,
			Keycode:   k.Keycode,
			Character: character,
		})
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})

	data := keysTemplateData{
		Keys:           views,
		Count:          len(views),
		Error:          keysError,
		EditingKeyName: editingKeyName,
		LayoutName:     layoutName,
	}

	var buf bytes.Buffer
	if err := keysTemplate.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] keys template error: %v\n", err)
		return ""
	}
	return buf.String()
}

// setKeyNameOverride adds or updates a user override, re-merges, saves, and re-pushes the store.
// Caller must NOT hold mu.
func setKeyNameOverride(name string, keycode uint16) error {
	mu.Lock()
	state.KeyNameOverrides[name] = keycode
	state.KeyNamesMerged[name] = keycode
	overrides := make(map[string]uint16, len(state.KeyNameOverrides))
	for k, v := range state.KeyNameOverrides {
		overrides[k] = v
	}
	merged := make(map[string]uint16, len(state.KeyNamesMerged))
	for k, v := range state.KeyNamesMerged {
		merged[k] = v
	}
	mu.Unlock()

	if err := saveKeyNameOverrides(overrides); err != nil {
		return err
	}
	return pushKeyNamesToStore(merged)
}

// deleteKeyNameOverride removes a user override, re-merges from defaults, saves, and re-pushes.
// Caller must NOT hold mu.
func deleteKeyNameOverride(name string) error {
	mu.Lock()
	delete(state.KeyNameOverrides, name)
	// Re-merge from defaults
	merged := make(map[string]uint16, len(state.KeyNameDefaults))
	for k, v := range state.KeyNameDefaults {
		merged[k] = v
	}
	for k, v := range state.KeyNameOverrides {
		merged[k] = v
	}
	state.KeyNamesMerged = merged
	overrides := make(map[string]uint16, len(state.KeyNameOverrides))
	for k, v := range state.KeyNameOverrides {
		overrides[k] = v
	}
	mergedCopy := make(map[string]uint16, len(merged))
	for k, v := range merged {
		mergedCopy[k] = v
	}
	mu.Unlock()

	if err := saveKeyNameOverrides(overrides); err != nil {
		return err
	}
	return pushKeyNamesToStore(mergedCopy)
}

func saveKeyNameOverrides(overrides map[string]uint16) error {
	appSupport := os.Getenv("BRANCHKIT_APP_SUPPORT")
	if appSupport == "" {
		return fmt.Errorf("BRANCHKIT_APP_SUPPORT not set")
	}
	path := filepath.Join(appSupport, "key_names.json")
	if len(overrides) == 0 {
		os.Remove(path)
		return nil
	}
	data, err := json.MarshalIndent(overrides, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func pushKeyNamesToStore(merged map[string]uint16) error {
	body := struct {
		Data map[string]uint16 `json:"data"`
	}{Data: merged}
	return platform.PostJSON("/v1/plugins/stores/key_names", body, "", nil)
}

type deleteKeyNameRequest struct {
	Name string `json:"name"`
}

func hookDeleteKeyName(w http.ResponseWriter, r *http.Request) {
	var req deleteKeyNameRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := deleteKeyNameOverride(req.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] delete key name error: %v\n", err)
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to delete key name: %v", err)
		mu.Unlock()
	}

	shared.WriteJSON(w, OkResponse{OK: err == nil})
}

type startEditKeyRequest struct {
	Name string `json:"name"`
}

func hookStartEditKey(w http.ResponseWriter, r *http.Request) {
	var req startEditKeyRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	state.EditingKeyName = req.Name
	mu.Unlock()

	shared.WriteJSON(w, OkResponse{OK: true})
}

func hookCancelEditKey(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	state.EditingKeyName = ""
	mu.Unlock()

	shared.WriteJSON(w, OkResponse{OK: true})
}

// hookEditKeyKeydown handles a keypress during key name editing.
// The user pressed a key to reassign what keycode a key name maps to.
// We parse the DOM event to get the BranchKit key name of the pressed key,
// look up its platform keycode, and save the override.
func hookEditKeyKeydown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code  string `json:"code"`
		Key   string `json:"key"`
		Ctrl  bool   `json:"ctrl"`
		Alt   bool   `json:"alt"`
		Shift bool   `json:"shift"`
		Meta  bool   `json:"meta"`
	}
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Atomically read and clear editing state — prevents concurrent edit races
	mu.Lock()
	editingName := state.EditingKeyName
	state.EditingKeyName = "" // claim this edit, any concurrent request sees empty
	mu.Unlock()

	if editingName == "" {
		shared.WriteJSON(w, OkResponse{OK: true})
		return
	}

	// Escape → cancel (editing already cleared above)
	if req.Key == "Escape" {
		shared.WriteJSON(w, OkResponse{OK: true})
		return
	}

	// Parse the DOM event
	parsed := parseDOMKeyEvent(DOMKeyEvent{
		Code: req.Code, Key: req.Key,
		CtrlKey: req.Ctrl, AltKey: req.Alt, ShiftKey: req.Shift, MetaKey: req.Meta,
	})

	// Bare modifier or unknown → ignore
	if parsed.IsBareModifier {
		shared.WriteJSON(w, OkResponse{OK: true})
		return
	}

	// Look up the pressed key's platform keycode from local state
	mu.Lock()
	newKeycode, found := state.KeyNamesMerged[parsed.KeyName]
	mu.Unlock()

	if !found {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Unknown key: %s", parsed.KeyName)
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	// Save override: existing name → new keycode
	if err := setKeyNameOverride(editingName, newKeycode); err != nil {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to update key: %v", err)
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	shared.WriteJSON(w, OkResponse{OK: true})
}
