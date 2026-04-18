package main

import (
	"testing"
)

func TestLocalKeyNames_NilState(t *testing.T) {
	mu.Lock()
	state.KeyNamesMerged = nil
	mu.Unlock()

	entries := localKeyNames()
	if entries != nil {
		t.Errorf("expected nil, got %d entries", len(entries))
	}
}

func TestLocalKeyNames_SourceAttribution(t *testing.T) {
	mu.Lock()
	state.KeyNamesMerged = map[string]uint16{"a": 0, "z": 6, "return": 36}
	mu.Unlock()

	entries := localKeyNames()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	byName := make(map[string]keyNameEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	if byName["a"].Source != "default" {
		t.Errorf("'a' source = %q, want 'default'", byName["a"].Source)
	}
	if byName["a"].Keycode != 0 {
		t.Errorf("'a' keycode = %d, want 0", byName["a"].Keycode)
	}
}

func TestBuildLayoutCharacters_USQwerty(t *testing.T) {
	keyNames := map[string]uint16{"a": 0, "z": 6, "return": 36}
	layoutMappings := map[string]string{"0": "a", "6": "z", "36": "\r"}

	chars := buildLayoutCharacters(keyNames, layoutMappings)

	if chars["a"] != "a" {
		t.Errorf("a = %q, want \"a\"", chars["a"])
	}
	if chars["z"] != "z" {
		t.Errorf("z = %q, want \"z\"", chars["z"])
	}
	if chars["return"] != "\r" {
		t.Errorf("return = %q, want \"\\r\"", chars["return"])
	}
}

func TestBuildLayoutCharacters_QWERTZ(t *testing.T) {
	keyNames := map[string]uint16{"y": 16, "z": 6}
	layoutMappings := map[string]string{"6": "y", "16": "z"}

	chars := buildLayoutCharacters(keyNames, layoutMappings)

	if chars["z"] != "y" {
		t.Errorf("z = %q, want \"y\" (QWERTZ)", chars["z"])
	}
	if chars["y"] != "z" {
		t.Errorf("y = %q, want \"z\" (QWERTZ)", chars["y"])
	}
}

func TestBuildLayoutCharacters_UnknownKeycodes(t *testing.T) {
	keyNames := map[string]uint16{"a": 0}
	layoutMappings := map[string]string{"0": "a", "99": "?"}

	chars := buildLayoutCharacters(keyNames, layoutMappings)

	if len(chars) != 1 {
		t.Errorf("expected 1 entry, got %d (unknown keycodes should be skipped)", len(chars))
	}
}

func TestBuildLayoutCharacters_Aliases(t *testing.T) {
	keyNames := map[string]uint16{"backslash": 42, "\\": 42}
	layoutMappings := map[string]string{"42": "\\"}

	chars := buildLayoutCharacters(keyNames, layoutMappings)

	if chars["backslash"] != "\\" {
		t.Errorf("backslash = %q, want \"\\\\\"", chars["backslash"])
	}
	if len(chars) != 2 {
		t.Errorf("expected 2 entries (both aliases), got %d", len(chars))
	}
}

func TestBuildLayoutCharacters_EmptyInputs(t *testing.T) {
	chars := buildLayoutCharacters(nil, map[string]string{"0": "a"})
	if len(chars) != 0 {
		t.Errorf("nil key names: expected 0 entries, got %d", len(chars))
	}

	chars = buildLayoutCharacters(map[string]uint16{"a": 0}, nil)
	if len(chars) != 0 {
		t.Errorf("nil layout: expected 0 entries, got %d", len(chars))
	}

	chars = buildLayoutCharacters(nil, nil)
	if len(chars) != 0 {
		t.Errorf("both nil: expected 0 entries, got %d", len(chars))
	}
}
