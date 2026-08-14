package main

import "testing"

// Every path that changes the effective bindings must re-register them with
// the platform — that is what forwards the new combos to the shell's
// KeybindCapture.
//
// Until 2026-08-14 the override paths didn't: remap and reset saved user
// overrides to `plugin.keyboard.overrides` and rebuilt the LOCAL registry,
// but the only re-registration trigger was the collection.updated
// subscription for the base `keybinds` collection, and overrides hit its
// `default: return`. So a remap looked successful in Settings while the shell
// kept firing the OLD combos until the next plugin restart. Found live: the
// user remapped the help toggle and the new key did nothing.
//
// These drive the real handlers with the collection IO and registration
// seams stubbed, and assert the rebuilt snapshot actually goes out.

func captureRegistrations(t *testing.T) *[]RegistrySnapshot {
	t.Helper()
	got := &[]RegistrySnapshot{}
	origRegister := registerKeybinds
	origLoad, origSave := loadUserKeybindOverrides, saveUserKeybindOverrides
	saved := map[string]string{}
	registerKeybinds = func(s RegistrySnapshot) { *got = append(*got, s) }
	loadUserKeybindOverrides = func() map[string]string {
		out := map[string]string{}
		for k, v := range saved {
			out[k] = v
		}
		return out
	}
	saveUserKeybindOverrides = func(o map[string]string) {
		saved = map[string]string{}
		for k, v := range o {
			saved[k] = v
		}
	}
	t.Cleanup(func() {
		registerKeybinds = origRegister
		loadUserKeybindOverrides, saveUserKeybindOverrides = origLoad, origSave
	})
	return got
}

func TestRemapRegistersTheNewBindings(t *testing.T) {
	// Seams first — buildRegistry itself reads the overrides.
	got := captureRegistrations(t)
	mu.Lock()
	state = newPluginState()
	state.KeybindsByPlugin = map[string]map[string]string{
		"voice": {"alt+h": "voice.help_toggle"},
	}
	state.rebuild()
	mu.Unlock()

	if _, err := handleRemap(&RemapRequest{
		OldCombo: "alt+h",
		NewCombo: "alt+z",
		IsHold:   false,
	}); err != nil {
		t.Fatalf("handleRemap: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("remap must register exactly once, got %d — an unregistered "+
			"remap leaves the shell firing the OLD combos", len(*got))
	}
	if a := findActionForCombo(&state.Registry, "alt+z"); a != "voice.help_toggle" {
		t.Fatalf("registered snapshot must carry the remapped combo, alt+z -> %q", a)
	}
}

func TestResetRegistersTheRestoredBindings(t *testing.T) {
	// Seams first — buildRegistry itself reads the overrides.
	got := captureRegistrations(t)
	mu.Lock()
	state = newPluginState()
	state.KeybindsByPlugin = map[string]map[string]string{
		"voice": {"alt+h": "voice.help_toggle"},
	}
	state.rebuild()
	mu.Unlock()

	if _, err := handleReset(&ResetRequest{ComboKey: "alt+z"}); err != nil {
		t.Fatalf("handleReset: %v", err)
	}
	if len(*got) != 1 {
		t.Fatal("reset changes the effective bindings and must register them")
	}

	if _, err := handleResetAll(nil); err != nil {
		t.Fatalf("handleResetAll: %v", err)
	}
	if len(*got) != 2 {
		t.Fatal("reset-all must register too")
	}
}
