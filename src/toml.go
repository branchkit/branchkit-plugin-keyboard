package main

import (
	"encoding/json"

	"github.com/branchkit/plugin-sdk-go"
)

func loadOverridesFromCollection() map[string]Binding {
	if plugin == nil {
		return make(map[string]Binding)
	}
	rec, err := plugin.Get("plugin.keyboard.overrides", "singleton")
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

func saveOverridesToCollection(overrides map[string]Binding) {
	if plugin == nil {
		return
	}
	if len(overrides) == 0 {
		plugin.Delete("plugin.keyboard.overrides", "singleton")
		return
	}
	if err := plugin.Put("plugin.keyboard.overrides", "singleton", overrides); err != nil {
		branchkit.Logf("keyboard", "failed to save overrides: %v", err)
	}
}
