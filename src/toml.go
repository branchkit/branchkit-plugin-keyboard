package main

import (
	"encoding/json"

	shared "github.com/branchkit/plugin-sdk-go"
)

func loadOverridesFromCollection() map[string]string {
	if plugin == nil {
		return make(map[string]string)
	}
	rec, err := plugin.Get("plugin.keyboard.overrides", "singleton")
	if err != nil {
		shared.Logf("keyboard", "overrides collection read error: %v", err)
		return make(map[string]string)
	}
	if rec != nil {
		var overrides map[string]string
		if err := json.Unmarshal(rec.Payload, &overrides); err != nil {
			shared.Logf("keyboard", "overrides collection parse error: %v", err)
			return make(map[string]string)
		}
		return overrides
	}
	return make(map[string]string)
}

func saveOverridesToCollection(overrides map[string]string) {
	if plugin == nil {
		return
	}
	if len(overrides) == 0 {
		plugin.Delete("plugin.keyboard.overrides", "singleton")
		return
	}
	if err := plugin.Put("plugin.keyboard.overrides", "singleton", overrides); err != nil {
		shared.Logf("keyboard", "failed to save overrides: %v", err)
	}
}

