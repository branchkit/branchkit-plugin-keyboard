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
	Keys  []keyNameView
	Count int
	Error string
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

	// Consume any pending error from hooks
	mu.Lock()
	keysError := state.KeysError
	state.KeysError = ""
	mu.Unlock()

	data := keysTemplateData{
		Keys:  views,
		Count: len(views),
		Error: keysError,
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "[keyboard] set key name error: %v\n", err)
		mu.Lock()
		state.KeysError = fmt.Sprintf("Failed to save key name: %v", err)
		mu.Unlock()
	}

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
