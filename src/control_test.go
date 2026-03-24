package main

import (
	"encoding/json"
	"testing"
)

func TestOkWithControlSerialization(t *testing.T) {
	resp := OkWithControl{
		OK: true,
		Control: &ControlDirectives{
			Signals: []string{"keybind:pause"},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)

	if m["ok"] != true {
		t.Fatal("expected ok=true")
	}
	ctrl, ok := m["_control"].(map[string]any)
	if !ok {
		t.Fatal("expected _control object")
	}
	signals, ok := ctrl["signals"].([]any)
	if !ok || len(signals) != 1 || signals[0] != "keybind:pause" {
		t.Fatalf("expected signals=[keybind:pause], got %v", ctrl["signals"])
	}
	// rebuild_stores should be omitted (omitempty)
	if _, exists := ctrl["rebuild_stores"]; exists {
		t.Fatal("rebuild_stores should be omitted when empty")
	}
}

func TestSnapshotWithControlSerialization(t *testing.T) {
	resp := SnapshotWithControl{
		Entries: []RegistryEntry{
			{Combo: "opt+t DOWN", Action: "voice dictation-start", Source: "plugin:voice"},
		},
		ListenUp: []string{"opt+t"},
		Control: &ControlDirectives{
			Signals:       []string{"keybind:resume"},
			RebuildStores: []string{"keybinds"},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)

	// Verify snapshot fields present
	entries, ok := m["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	listenUp, ok := m["listen_up"].([]any)
	if !ok || len(listenUp) != 1 {
		t.Fatal("expected 1 listen_up")
	}

	// Verify _control
	ctrl, ok := m["_control"].(map[string]any)
	if !ok {
		t.Fatal("expected _control object")
	}
	signals := ctrl["signals"].([]any)
	if len(signals) != 1 || signals[0] != "keybind:resume" {
		t.Fatalf("expected signals=[keybind:resume], got %v", signals)
	}
	stores := ctrl["rebuild_stores"].([]any)
	if len(stores) != 1 || stores[0] != "keybinds" {
		t.Fatalf("expected rebuild_stores=[keybinds], got %v", stores)
	}
}

func TestOkWithControlNoDirectives(t *testing.T) {
	resp := OkWithControl{OK: true}
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)

	// _control should be omitted when nil
	if _, exists := m["_control"]; exists {
		t.Fatal("_control should be omitted when nil")
	}
}
