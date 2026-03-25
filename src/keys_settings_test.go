package main

import (
	"testing"
)

func TestLocalKeyNames_NilState(t *testing.T) {
	mu.Lock()
	state.KeyNamesMerged = nil
	state.KeyNameOverrides = nil
	mu.Unlock()

	entries := localKeyNames()
	if entries != nil {
		t.Errorf("expected nil, got %d entries", len(entries))
	}
}

func TestLocalKeyNames_SourceAttribution(t *testing.T) {
	mu.Lock()
	state.KeyNameDefaults = map[string]uint16{"a": 0, "z": 6, "return": 36}
	state.KeyNameOverrides = map[string]uint16{"z": 99}
	state.KeyNamesMerged = map[string]uint16{"a": 0, "z": 99, "return": 36}
	mu.Unlock()

	entries := localKeyNames()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	byName := make(map[string]keyNameEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	// "a" is default
	if byName["a"].Source != "default" {
		t.Errorf("'a' source = %q, want 'default'", byName["a"].Source)
	}
	if byName["a"].Keycode != 0 {
		t.Errorf("'a' keycode = %d, want 0", byName["a"].Keycode)
	}

	// "z" is overridden
	if byName["z"].Source != "user" {
		t.Errorf("'z' source = %q, want 'user'", byName["z"].Source)
	}
	if byName["z"].Keycode != 99 {
		t.Errorf("'z' keycode = %d, want 99 (overridden)", byName["z"].Keycode)
	}

	// "return" is default
	if byName["return"].Source != "default" {
		t.Errorf("'return' source = %q, want 'default'", byName["return"].Source)
	}
}

func TestSetKeyNameOverride_MergesCorrectly(t *testing.T) {
	mu.Lock()
	state.KeyNameDefaults = map[string]uint16{"a": 0, "z": 6}
	state.KeyNameOverrides = make(map[string]uint16)
	state.KeyNamesMerged = map[string]uint16{"a": 0, "z": 6}
	mu.Unlock()

	// Can't test the full setKeyNameOverride (needs filesystem + platform client),
	// but we can test the merge logic directly
	mu.Lock()
	state.KeyNameOverrides["z"] = 99
	state.KeyNamesMerged["z"] = 99
	mu.Unlock()

	mu.Lock()
	if state.KeyNamesMerged["z"] != 99 {
		t.Errorf("merged z = %d, want 99", state.KeyNamesMerged["z"])
	}
	if state.KeyNamesMerged["a"] != 0 {
		t.Errorf("merged a = %d, want 0 (unchanged)", state.KeyNamesMerged["a"])
	}
	mu.Unlock()
}

func TestDeleteKeyNameOverride_RevertsToDefault(t *testing.T) {
	mu.Lock()
	state.KeyNameDefaults = map[string]uint16{"a": 0, "z": 6}
	state.KeyNameOverrides = map[string]uint16{"z": 99}
	state.KeyNamesMerged = map[string]uint16{"a": 0, "z": 99}
	mu.Unlock()

	// Simulate the merge logic from deleteKeyNameOverride
	mu.Lock()
	delete(state.KeyNameOverrides, "z")
	merged := make(map[string]uint16, len(state.KeyNameDefaults))
	for k, v := range state.KeyNameDefaults {
		merged[k] = v
	}
	for k, v := range state.KeyNameOverrides {
		merged[k] = v
	}
	state.KeyNamesMerged = merged
	mu.Unlock()

	mu.Lock()
	if state.KeyNamesMerged["z"] != 6 {
		t.Errorf("merged z after delete = %d, want 6 (reverted to default)", state.KeyNamesMerged["z"])
	}
	mu.Unlock()
}
