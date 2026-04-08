package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	shared "github.com/branchkit/plugin-sdk-go"
)

// Minimal TOML parser for the keybinds.toml file format:
//   [overrides]
//   "key" = "value"

func loadOverridesFromTOML(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]string)
	}
	return parseSimpleTOML(string(data))
}

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

func saveOverridesToTOML(overrides map[string]string, path string) {
	if len(overrides) == 0 {
		_ = os.Remove(path)
		return
	}

	var b strings.Builder
	b.WriteString("[overrides]\n")

	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := overrides[key]
		escapedKey := strings.ReplaceAll(key, `\`, `\\`)
		escapedKey = strings.ReplaceAll(escapedKey, `"`, `\"`)
		escapedValue := strings.ReplaceAll(value, `\`, `\\`)
		escapedValue = strings.ReplaceAll(escapedValue, `"`, `\"`)
		fmt.Fprintf(&b, "\"%s\" = \"%s\"\n", escapedKey, escapedValue)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		shared.Logf("keyboard", "failed to write keybinds.toml: %v", err)
	}
}

func removeOverridesFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
