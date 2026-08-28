package main

import (
	"encoding/json"
	"testing"
)

// The overrides collection and the keybinds records both carry Binding:
// an exact dotted action type plus params. The tombstone (empty action)
// round-trips, and params survive marshaling untouched.
func TestBindingJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Binding
	}{
		{"tombstone", `{"action":""}`, Binding{}},
		{"with params", `{"action":"scripts.run","params":{"script":"notes.lua","handler":"h1"}}`,
			Binding{Action: "scripts.run", Params: json.RawMessage(`{"script":"notes.lua","handler":"h1"}`)}},
		{"without params", `{"action":"voice.dictation"}`, Binding{Action: "voice.dictation"}},
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

func TestHumanizeActionDottedTypes(t *testing.T) {
	cases := map[string]string{
		"voice.dictation": "Dictation",
		"tiling.move_to":  "Move to",
		"scripts.run":     "Run",
	}
	for in, want := range cases {
		if got := humanizeAction(in); got != want {
			t.Fatalf("humanizeAction(%q) = %q, want %q", in, got, want)
		}
	}
}
