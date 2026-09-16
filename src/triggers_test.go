package main

import (
	"encoding/json"
	"testing"
)

// Every binding becomes one keycap trigger record for its action, labeled as
// the Keybinds tab spells the combo; the up half of a hold does not.
func TestTriggerEntriesAreOneKeycapPerLiveBinding(t *testing.T) {
	reg := newRegistry()
	down, _ := parseCombo("opt+shift+h")
	reg.Entries[comboKey(down)] = KeybindEntry{
		Combo:  down,
		Action: "helloworld.greet",
		Params: json.RawMessage(`{"name":"BranchKit"}`),
		Source: KeybindSource{PluginID: "helloworld"},
	}
	hold, _ := parseCombo("cmd+space down")
	reg.Entries[comboKey(hold)] = KeybindEntry{Combo: hold, Action: "voice.dictation"}
	up, _ := parseCombo("cmd+space up")
	reg.Entries[comboKey(up)] = KeybindEntry{Combo: up, Action: "voice.dictation"}

	entries := triggerEntries(reg)
	if len(entries) != 2 {
		t.Fatalf("want 2 triggers (press + hold), got %d: %+v", len(entries), entries)
	}
	var holdRec triggerRecord
	if err := json.Unmarshal(entries[0].Payload, &holdRec); err != nil {
		t.Fatal(err)
	}
	if holdRec.ID != "keyboard:cmd+space" || holdRec.Label != "cmd+space (hold)" {
		t.Fatalf("hold record: %+v", holdRec)
	}
	var rec triggerRecord
	if err := json.Unmarshal(entries[1].Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ID != "keyboard:opt+shift+h" || rec.Label != "opt+shift+h" || rec.Form != "keycap" ||
		rec.Action != "helloworld.greet" || rec.Home != "keybinds" || string(rec.Params) != `{"name":"BranchKit"}` {
		t.Fatalf("unexpected record: %+v", rec)
	}
}
