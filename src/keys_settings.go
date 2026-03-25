package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"sort"
	"strings"

	"branchkit.local/shared"
)

//go:embed templates/keys.html
var keysTemplateHTML string

var keysTemplate = template.Must(template.New("keys").Funcs(template.FuncMap{
	"eq": func(a, b string) bool { return a == b },
}).Parse(keysTemplateHTML))

type keyNameEntry struct {
	Name    string `json:"name"`
	Keycode uint16 `json:"keycode"`
	Source  string `json:"source"`
}

type keyNameView struct {
	Name        string
	NameJSON    template.JS // JSON-escaped name safe for use in Datastar expressions
	Keycode     uint16
	Character   string
	Source      string
	BadgeClass  string
	SourceLabel string
}

type keysTemplateData struct {
	Keys           []keyNameView
	Count          int
	Error          string
	EditingKeyName string // name being edited (empty = not editing)
	LayoutName     string // detected OS keyboard layout name
}

func fetchKeyNames() ([]keyNameEntry, error) {
	var resp struct {
		Keys []keyNameEntry `json:"keys"`
	}
	err := platform.GetJSON("/v1/key-names", &resp)
	if err != nil {
		return nil, err
	}
	return resp.Keys, nil
}

type layoutResponse struct {
	LayoutID   string            `json:"layout_id"`
	LayoutName string            `json:"layout_name"`
	Mappings   map[string]string `json:"mappings"`
}

func fetchKeyboardLayout() (*layoutResponse, error) {
	var resp layoutResponse
	err := platform.GetJSON("/v1/native/keyboard-layout", &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func renderKeysSettings(search string) string {
	keys, err := fetchKeyNames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] failed to fetch key names: %v\n", err)
		keys = nil
	}

	layout, err := fetchKeyboardLayout()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] failed to fetch keyboard layout: %v\n", err)
		layout = &layoutResponse{Mappings: map[string]string{}}
	}

	var views []keyNameView
	for _, k := range keys {
		if search != "" && !strings.Contains(strings.ToLower(k.Name), search) {
			continue
		}

		character := layout.Mappings[fmt.Sprintf("%d", k.Keycode)]
		if character == "" {
			character = "–"
		}

		badgeClass := "badge-core"
		sourceLabel := "Default"
		if k.Source == "user" {
			badgeClass = "badge-user"
			sourceLabel = "User"
		}

		// JSON-encode name for safe interpolation in Datastar expressions
		nameBytes, _ := json.Marshal(k.Name)
		nameJSON := template.JS(string(nameBytes))

		views = append(views, keyNameView{
			Name:        k.Name,
			NameJSON:    nameJSON,
			Keycode:     k.Keycode,
			Character:   character,
			Source:      k.Source,
			BadgeClass:  badgeClass,
			SourceLabel: sourceLabel,
		})
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})

	// Consume any pending error and editing state from hooks
	mu.Lock()
	keysError := state.KeysError
	state.KeysError = ""
	editingKeyName := state.EditingKeyName
	mu.Unlock()

	layoutName := layout.LayoutName
	if layoutName == "" {
		layoutName = "Unknown"
	}

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

type setKeyNameRequest struct {
	Name    string `json:"name"`
	Keycode uint16 `json:"keycode"`
}

type deleteKeyNameRequest struct {
	Name string `json:"name"`
}

func hookSetKeyName(w http.ResponseWriter, r *http.Request) {
	var req setKeyNameRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		mu.Lock()
		state.KeysError = "Key name must not be empty."
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	err := platform.PostJSON("/v1/key-names/override", map[string]any{
		"action":  "set",
		"name":    req.Name,
		"keycode": req.Keycode,
	}, "", nil)
	mu.Lock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] set key name error: %v\n", err)
		state.KeysError = fmt.Sprintf("Failed to save key name: %v", err)
	}
	state.EditingKeyName = "" // clear editing state on save attempt
	mu.Unlock()

	shared.WriteJSON(w, OkResponse{OK: err == nil})
}

func hookDeleteKeyName(w http.ResponseWriter, r *http.Request) {
	var req deleteKeyNameRequest
	if err := shared.ReadJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := platform.PostJSON("/v1/key-names/override", map[string]any{
		"action": "delete",
		"name":   req.Name,
	}, "", nil)
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

	// Look up the pressed key's platform keycode from the key name map
	var keysResp struct {
		Keys []keyNameEntry `json:"keys"`
	}
	if err := platform.GetJSON("/v1/key-names", &keysResp); err != nil {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to look up keycodes: %v", err)
		state.EditingKeyName = ""
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	var newKeycode uint16
	for _, k := range keysResp.Keys {
		if k.Name == parsed.KeyName {
			newKeycode = k.Keycode
			break
		}
	}

	if newKeycode == 0 {
		mu.Lock()
		state.KeysError = fmt.Sprintf("Unknown key: %s", parsed.KeyName)
		state.EditingKeyName = ""
		mu.Unlock()
		shared.WriteJSON(w, OkResponse{OK: false})
		return
	}

	// Save override: existing name → new keycode
	err := platform.PostJSON("/v1/key-names/override", map[string]any{
		"action":  "set",
		"name":    editingName,
		"keycode": newKeycode,
	}, "", nil)

	mu.Lock()
	if err != nil {
		state.KeysError = fmt.Sprintf("Failed to update key: %v", err)
	}
	state.EditingKeyName = ""
	mu.Unlock()

	shared.WriteJSON(w, OkResponse{OK: err == nil})
}
