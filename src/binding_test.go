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

// The bind-a-command flow end to end at the handler level: open the picker
// (fetch stubbed), choose a candidate, press a combo — the override is
// saved carrying the command's action AND params, the new registry is
// registered exactly once, and the combo resolves to the binding.
// stubParseKeyEvent stands in for `input.parse_key_event`.
//
// The parse used to be local and pure, so these handlers were testable with
// no host; it is a platform operation now, and reaching it needs a live RPC.
// Stubbing is the honest trade — what these tests cover is the bind FLOW
// (picker, choose, keydown, registration), and the parse itself is pinned in
// the actuator against the key registry the local copy disagreed with.
func stubParseKeyEvent(t *testing.T, out ParsedKeyEvent) {
	t.Helper()
	orig := parseKeyEvent
	parseKeyEvent = func(DOMKeyEvent) (ParsedKeyEvent, error) { return out, nil }
	t.Cleanup(func() { parseKeyEvent = orig })
}

func TestBindACommandFlow(t *testing.T) {
	got := captureRegistrations(t)

	origFetch := fetchBindableCommands
	fetchBindableCommands = func() ([]bindCandidate, error) {
		return []bindCandidate{{
			ID:      "scripts:bind probe check",
			Pattern: "bind probe check",
			Owner:   "scripts",
			B:       Binding{Action: "scripts.run", Params: json.RawMessage(`{"script":"bindprobe","handler":"h1"}`)},
		}}, nil
	}
	t.Cleanup(func() { fetchBindableCommands = origFetch })

	mu.Lock()
	state = newPluginState()
	mu.Unlock()

	if _, err := handleOpenBindPicker(nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := handleChooseBind(&ChooseBindRequest{ID: "scripts:bind probe check"}); err != nil {
		t.Fatalf("choose: %v", err)
	}
	stubParseKeyEvent(t, ParsedKeyEvent{Combo: "ctrl+opt+z", KeyName: "z", HasModifiers: true})
	if _, err := handleBindKeydown(&BindKeydownRequest{DOMKeyEvent: DOMKeyEvent{
		Code: "KeyZ", Key: "z", AltKey: true, CtrlKey: true,
	}}); err != nil {
		t.Fatalf("keydown: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("bind must register exactly once, got %d", len(*got))
	}
	b := findActionForCombo(&state.Registry, "ctrl+opt+z")
	if b.Action != "scripts.run" {
		t.Fatalf("combo must resolve to the bound action, got %+v", b)
	}
	if string(b.Params) != `{"script":"bindprobe","handler":"h1"}` {
		t.Fatalf("params must ride the binding, got %s", b.Params)
	}
	mu.Lock()
	if state.BindPicker != nil || state.PendingBind != nil {
		mu.Unlock()
		t.Fatal("picker must close after a successful bind")
	}
	mu.Unlock()
}

// A combo without modifiers is refused with a one-shot error and no write.
func TestBindKeydownRequiresModifiers(t *testing.T) {
	got := captureRegistrations(t)
	origFetch := fetchBindableCommands
	fetchBindableCommands = func() ([]bindCandidate, error) {
		return []bindCandidate{{ID: "x", Pattern: "x", Owner: "p", B: Binding{Action: "p.x"}}}, nil
	}
	t.Cleanup(func() { fetchBindableCommands = origFetch })

	mu.Lock()
	state = newPluginState()
	mu.Unlock()
	handleOpenBindPicker(nil)
	handleChooseBind(&ChooseBindRequest{ID: "x"})
	stubParseKeyEvent(t, ParsedKeyEvent{Combo: "z", KeyName: "z"})
	handleBindKeydown(&BindKeydownRequest{DOMKeyEvent: DOMKeyEvent{Code: "KeyZ", Key: "z"}})

	if len(*got) != 0 {
		t.Fatal("a refused bind must not register")
	}
	mu.Lock()
	defer mu.Unlock()
	if state.BindError == "" {
		t.Fatal("refusal must set the one-shot error")
	}
	if state.PendingBind == nil {
		t.Fatal("capture stays open so the user can try again")
	}
}
