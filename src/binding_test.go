package main

import (
	"encoding/json"
	"testing"
)

// The overrides collection and the keybinds records both carry Binding.
// A bare JSON string is an action with no params — records and overrides
// written before params existed parse unchanged, and the tombstone ("")
// round-trips.
func TestBindingJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Binding
	}{
		{"bare string", `"voice dictation"`, Binding{Action: "voice dictation"}},
		{"tombstone", `""`, Binding{}},
		{"object with params", `{"action":"scripts.run","params":{"script":"notes.lua","handler":3}}`,
			Binding{Action: "scripts.run", Params: json.RawMessage(`{"script":"notes.lua","handler":3}`)}},
		{"object without params", `{"action":"voice dictation"}`, Binding{Action: "voice dictation"}},
	}
	for _, c := range cases {
		var b Binding
		if err := json.Unmarshal([]byte(c.in), &b); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		if b.Action != c.want.Action {
			t.Fatalf("%s: action %q, want %q", c.name, b.Action, c.want.Action)
		}
		if (len(b.Params) == 0) != (len(c.want.Params) == 0) {
			t.Fatalf("%s: params presence mismatch: %s", c.name, b.Params)
		}

		out, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.name, err)
		}
		var back Binding
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatalf("%s: re-unmarshal %s: %v", c.name, out, err)
		}
		if back.Action != b.Action {
			t.Fatalf("%s: round-trip action %q -> %q", c.name, b.Action, back.Action)
		}
	}

	// A param-less binding marshals as a bare string — the shape overrides
	// have always had on disk.
	out, _ := json.Marshal(Binding{Action: "voice dictation"})
	if string(out) != `"voice dictation"` {
		t.Fatalf("param-less binding must marshal as a bare string, got %s", out)
	}
}

// Params flow from the per-plugin map through buildRegistry into the
// snapshot the actuator caches.
func TestBuildRegistryCarriesParams(t *testing.T) {
	origLoad := loadUserKeybindOverrides
	loadUserKeybindOverrides = func() map[string]Binding { return map[string]Binding{} }
	defer func() { loadUserKeybindOverrides = origLoad }()

	reg := buildRegistry(map[string]map[string]Binding{
		"scripts": {"opt+n": {Action: "scripts.run", Params: json.RawMessage(`{"script":"notes.lua"}`)}},
	})
	snap := reg.toSnapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap.Entries))
	}
	e := snap.Entries[0]
	if e.Action != "scripts.run" || string(e.Params) != `{"script":"notes.lua"}` {
		t.Fatalf("params lost: %+v", e)
	}
}
