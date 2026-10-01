package main

import (
	"encoding/json"
	"sort"
	"testing"
)

// The platform derives the hotkey table from contributed bindings and the
// user's edits, and re-derives inside every write (tested in the actuator,
// state::bindings). What this plugin owns is the edit: remap, reset and
// bind-a-command must SAVE the right edits, and the tab must show the table
// that results. These drive the real handlers against a fake platform: the
// contributed bindings, the saved edits, and the table derived from them.

type fakePlatform struct {
	contributed map[string]map[string]Binding // plugin → trigger → binding
	saved       map[string]Binding            // trigger → edit (empty Action = unbind)
	saves       int
}

// derive is the fake's table: contributed bindings, plugins in id order, first
// wins; edits on top. Enough for flows without collisions.
func (f *fakePlatform) derive() activeTable {
	type row struct {
		trigger string
		b       Binding
		plugin  string // "" = the user's edit
	}
	table := map[string]row{}
	plugins := make([]string, 0, len(f.contributed))
	for p := range f.contributed {
		plugins = append(plugins, p)
	}
	sort.Strings(plugins)
	for _, p := range plugins {
		for trig, b := range f.contributed[p] {
			c, _ := parseCombo(trig)
			if _, taken := table[comboKey(c)]; !taken {
				table[comboKey(c)] = row{trig, b, p}
			}
		}
	}
	for trig, b := range f.saved {
		c, _ := parseCombo(trig)
		if b.IsZero() {
			delete(table, comboKey(c))
			continue
		}
		table[comboKey(c)] = row{trig, b, ""}
	}
	var t activeTable
	for _, r := range table {
		t.Entries = append(t.Entries, struct {
			Trigger  string          `json:"trigger"`
			Action   string          `json:"action"`
			Params   json.RawMessage `json:"params,omitempty"`
			ByUser   bool            `json:"by_user"`
			PluginID string          `json:"plugin_id"`
		}{r.trigger, r.b.Action, r.b.Params, r.plugin == "", r.plugin})
	}
	return t
}

func withFakePlatform(h *Host, t *testing.T, contributed map[string]map[string]Binding) *fakePlatform {
	t.Helper()
	f := &fakePlatform{contributed: contributed, saved: map[string]Binding{}}
	origActive, origContrib := h.fetchActive, h.fetchContributed
	origLoad, origSave := h.loadUserKeybindOverrides, h.saveUserKeybindOverrides
	h.fetchActive = func() (activeTable, error) { return f.derive(), nil }
	h.fetchContributed = func() ([]contributedBinding, error) {
		var out []contributedBinding
		for _, binds := range f.contributed {
			for trig, b := range binds {
				out = append(out, contributedBinding{Trigger: trig, Binding: b})
			}
		}
		return out, nil
	}
	h.loadUserKeybindOverrides = func() map[string]Binding {
		out := map[string]Binding{}
		for k, v := range f.saved {
			out[k] = v
		}
		return out
	}
	h.saveUserKeybindOverrides = func(o map[string]Binding) {
		f.saves++
		f.saved = map[string]Binding{}
		for k, v := range o {
			f.saved[k] = v
		}
	}
	t.Cleanup(func() {
		h.fetchActive, h.fetchContributed = origActive, origContrib
		h.loadUserKeybindOverrides, h.saveUserKeybindOverrides = origLoad, origSave
	})
	h.mu.Lock()
	h.state = newPluginState()
	h.state.rebuild(h)
	h.mu.Unlock()
	return f
}

// A remap moves the action to the new combo and unbinds the old one, as two
// saved edits — and the tab shows the result without a restart. Until
// 2026-08-14 a remap looked successful here while the shell kept firing the
// OLD combo; with the platform deriving the table inside the write, saving
// the edit is what applies it.
func TestRemapSavesTheEditsAndShowsThem(t *testing.T) {
	h := newTestHost()
	f := withFakePlatform(h, t, map[string]map[string]Binding{
		"voice": {"alt+h": {Action: "voice.help_toggle"}},
	})

	if _, err := h.handleRemap(&RemapRequest{OldCombo: "alt+h", NewCombo: "alt+z"}); err != nil {
		t.Fatalf("handleRemap: %v", err)
	}

	if got := f.saved["alt+z"]; got.Action != "voice.help_toggle" {
		t.Fatalf("the new combo must be saved with the action, got %+v", got)
	}
	if got, ok := f.saved["alt+h"]; !ok || !got.IsZero() {
		t.Fatalf("the old combo must be saved as unbound, got %+v (present %v)", got, ok)
	}
	if a := findActionForCombo(&h.state.Registry, "alt+z"); a.Action != "voice.help_toggle" {
		t.Fatalf("the tab must show the remapped combo, alt+z -> %q", a.Action)
	}
	if a := findActionForCombo(&h.state.Registry, "alt+h"); !a.IsZero() {
		t.Fatalf("the old combo must be gone from the tab, alt+h -> %q", a.Action)
	}
}

// Reset on the remapped combo lifts both edits: the plugin's own binding is
// what it restores.
func TestResetRestoresThePluginBinding(t *testing.T) {
	h := newTestHost()
	f := withFakePlatform(h, t, map[string]map[string]Binding{
		"voice": {"alt+h": {Action: "voice.help_toggle"}},
	})
	if _, err := h.handleRemap(&RemapRequest{OldCombo: "alt+h", NewCombo: "alt+z"}); err != nil {
		t.Fatalf("handleRemap: %v", err)
	}

	if err := h.handleReset(&ResetRequest{ComboKey: "alt+z"}); err != nil {
		t.Fatalf("handleReset: %v", err)
	}
	if len(f.saved) != 0 {
		t.Fatalf("reset must lift the remap and the unbind it left, saved %v", f.saved)
	}
	if a := findActionForCombo(&h.state.Registry, "alt+h"); a.Action != "voice.help_toggle" {
		t.Fatalf("the plugin binding is back, alt+h -> %q", a.Action)
	}

	f.saved["cmd+x"] = Binding{}
	if err := h.handleResetAll(nil); err != nil {
		t.Fatalf("handleResetAll: %v", err)
	}
	if len(f.saved) != 0 {
		t.Fatalf("reset-all clears every edit, saved %v", f.saved)
	}
}
