package main

import (
	"fmt"
	"sort"
	"strings"
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

	return renderTempl(KeysSettings(data))
}
