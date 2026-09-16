package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

type keyNameEntry struct {
	Name    string `json:"name"`
	Keycode uint16 `json:"keycode"`
	Source  string `json:"source"`
}

type keyNameView struct {
	Name      string
	Keycode   uint16
	Character string
}

type keysTemplateData struct {
	Keys       []keyNameView
	Count      int
	Error      string
	LayoutName string // detected OS keyboard layout name
}

// localKeyNames returns key name entries from the plugin's in-memory state.
// No actuator call needed — the keyboard plugin owns this data.
func (h *Host) localKeyNames() []keyNameEntry {
	h.mu.Lock()
	if h.state.KeyNamesMerged == nil {
		h.mu.Unlock()
		return nil
	}
	entries := make([]keyNameEntry, 0, len(h.state.KeyNamesMerged))
	for name, keycode := range h.state.KeyNamesMerged {
		entries = append(entries, keyNameEntry{Name: name, Keycode: keycode, Source: "default"})
	}
	h.mu.Unlock()
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

func (h *Host) renderKeysSettings(search string) (string, error) {
	keys := h.localKeyNames()

	// Read layout data from local state (cached at startup)
	h.mu.Lock()
	layoutMappings := h.state.LayoutMappings
	layoutName := h.state.LayoutName
	keysError := h.state.KeysError
	h.state.KeysError = ""
	h.mu.Unlock()

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

		views = append(views, keyNameView{
			Name:      k.Name,
			Keycode:   k.Keycode,
			Character: character,
		})
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})

	data := keysTemplateData{
		Keys:       views,
		Count:      len(views),
		Error:      keysError,
		LayoutName: layoutName,
	}

	return branchkit.RenderComponent(KeysSettings(data))
}
