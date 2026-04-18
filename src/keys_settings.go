package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"

	shared "github.com/branchkit/plugin-sdk-go"
)

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
	entries := make([]keyNameEntry, 0, len(state.KeyNamesMerged))
	for name, keycode := range state.KeyNamesMerged {
		entries = append(entries, keyNameEntry{Name: name, Keycode: keycode, Source: "default"})
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

	return renderTempl(KeysSettings(data))
}

// setKeyNameOverride adds or updates a user override, re-merges, saves, and re-pushes the store.
// Caller must NOT hold mu.
// overrideKeycode calls the platform collection.override RPC to persist a keycode override.
func overrideKeycode(name string, keycode uint16) error {
	return plugin.Call("collection.override", map[string]any{
		"collection": "keycodes",
		"action":     "add",
		"key":        name,
		"value":      fmt.Sprintf("%d", keycode),
	}, nil)
}

type deleteKeyNameRequest struct {
	Name string `json:"name"`
}

func handleDeleteKeyName(req *deleteKeyNameRequest) (any, error) {
	err := plugin.Call("collection.override", map[string]any{
		"collection": "keycodes",
		"action":     "remove",
		"key":        req.Name,
	}, nil)
	if err != nil {
		shared.Logf("keyboard", "delete key name error: %v", err)
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to delete key name: %v", err)
		mu.Unlock()
	}

	return OkResponse{OK: err == nil}, nil
}

type startEditKeyRequest struct {
	Name string `json:"name"`
}

func handleStartEditKey(req *startEditKeyRequest) (any, error) {
	mu.Lock()
	state.EditingKeyName = req.Name
	mu.Unlock()

	return OkResponse{OK: true}, nil
}

func handleCancelEditKey(_ *struct{}) (any, error) {
	mu.Lock()
	state.EditingKeyName = ""
	mu.Unlock()

	return OkResponse{OK: true}, nil
}

type editKeyKeydownRequest struct {
	Code  string `json:"code"`
	Key   string `json:"key"`
	Ctrl  bool   `json:"ctrl"`
	Alt   bool   `json:"alt"`
	Shift bool   `json:"shift"`
	Meta  bool   `json:"meta"`
}

// handleEditKeyKeydown handles a keypress during key name editing.
func handleEditKeyKeydown(req *editKeyKeydownRequest) (any, error) {
	// Atomically read and clear editing state
	mu.Lock()
	editingName := state.EditingKeyName
	state.EditingKeyName = ""
	mu.Unlock()

	if editingName == "" {
		return OkResponse{OK: true}, nil
	}

	if req.Key == "Escape" {
		return OkResponse{OK: true}, nil
	}

	parsed := parseDOMKeyEvent(DOMKeyEvent{
		Code: req.Code, Key: req.Key,
		CtrlKey: req.Ctrl, AltKey: req.Alt, ShiftKey: req.Shift, MetaKey: req.Meta,
	})

	if parsed.IsBareModifier {
		return OkResponse{OK: true}, nil
	}

	mu.Lock()
	newKeycode, found := state.KeyNamesMerged[parsed.KeyName]
	mu.Unlock()

	if !found {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Unknown key: %s", parsed.KeyName)
		mu.Unlock()
		return OkResponse{OK: false}, nil
	}

	if err := overrideKeycode(editingName, newKeycode); err != nil {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to update key: %v", err)
		mu.Unlock()
		return OkResponse{OK: false}, nil
	}

	return OkResponse{OK: true}, nil
}
