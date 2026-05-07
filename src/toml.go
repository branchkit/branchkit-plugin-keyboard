package main

import (
	"encoding/json"
	"os"
	"strings"

	shared "github.com/branchkit/plugin-sdk-go"
)

// loadOverridesFromCollection reads user keybind overrides from the collection system.
// Falls back to migrating from the legacy keybinds.toml file.
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
	return migrateOverridesFromFile()
}

func migrateOverridesFromFile() map[string]string {
	appSupport := os.Getenv("BRANCHKIT_APP_SUPPORT")
	if appSupport == "" {
		return make(map[string]string)
	}
	path := appSupport + "/keybinds.toml"
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]string)
	}
	overrides := parseSimpleTOML(string(data))
	if len(overrides) > 0 {
		shared.Logf("keyboard", "migrated keybinds.toml → plugin.keyboard.overrides collection")
		saveOverridesToCollection(overrides)
	}
	return overrides
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

// --- Legacy TOML parsing (used only for migration) ---

func parseSimpleTOML(content string) map[string]string {
	result := make(map[string]string)
	inOverrides := false

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if line == "[overrides]" {
			inOverrides = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inOverrides = false
			continue
		}
		if !inOverrides {
			continue
		}

		// Parse "key" = "value"
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])

		key = unquoteTOML(key)
		value = unquoteTOML(value)

		result[key] = value
	}

	return result
}

func unquoteTOML(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

