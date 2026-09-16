package main

import (
	"encoding/json"

	"github.com/branchkit/plugin-sdk-go"
)

func (h *Host) loadOverridesFromCollection() map[string]Binding {
	if h.plugin == nil {
		return make(map[string]Binding)
	}
	rec, err := h.plugin.Get("plugin.keyboard.overrides", "singleton")
	if err != nil {
		branchkit.Logf("keyboard", "overrides collection read error: %v", err)
		return make(map[string]Binding)
	}
	if rec != nil {
		var overrides map[string]Binding
		if err := json.Unmarshal(rec.Payload, &overrides); err != nil {
			branchkit.Logf("keyboard", "overrides collection parse error: %v", err)
			return make(map[string]Binding)
		}
		return overrides
	}
	return make(map[string]Binding)
}

func (h *Host) saveOverridesToCollection(overrides map[string]Binding) {
	if h.plugin == nil {
		return
	}
	if len(overrides) == 0 {
		h.plugin.Delete("plugin.keyboard.overrides", "singleton")
		return
	}
	if err := h.plugin.Put("plugin.keyboard.overrides", "singleton", overrides); err != nil {
		branchkit.Logf("keyboard", "failed to save overrides: %v", err)
	}
}
