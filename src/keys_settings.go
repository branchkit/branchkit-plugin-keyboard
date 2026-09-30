package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

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

// isPrintable reports whether a layout character can be shown as itself in
// the keys table's "Your Keyboard" cell; renderKeysSettings shows "–"
// otherwise. False for an empty string, any control character (C0, DEL, C1),
// and a whitespace-only string: the cell is an HTML text node, where the
// space bar's " " collapses to an empty-looking cell just as "" would.
// Whitespace beside visible characters is fine.
func isPrintable(s string) bool {
	visible := false
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
		if !unicode.IsSpace(r) {
			visible = true
		}
	}
	return visible
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
		if !isPrintable(character) {
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
