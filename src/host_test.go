package main

import (
	"testing"

	"github.com/branchkit/plugin-sdk-go"
)

// newTestHost is a host on a detached plugin: no platform behind it, so a
// handler that reaches for one fails at once instead of being skipped by a
// nil guard. Tests that need an answer replace the seam they need.
func newTestHost() *Host { return newHost(branchkit.NewDetachedPlugin()) }

// A failed read of the saved overrides (not an absent record) marks them
// unreadable, and saving is refused until a read succeeds: every remap is
// load, edit, save the whole map, so a save after a failed read would
// replace the user's overrides with just the one edit.
func TestOverridesAreNotSavedAfterAFailedRead(t *testing.T) {
	h := newTestHost() // detached: every read fails
	got := h.loadOverridesFromCollection()
	if len(got) != 0 {
		t.Fatalf("a failed read yields an empty map to edit, got %v", got)
	}
	if !h.overridesUnreadable.Load() {
		t.Fatal("a failed read must mark the saved overrides unreadable")
	}
	// Refused before any RPC: a detached plugin would fail the Put anyway,
	// so what's asserted is that the guard, not the transport, stops it.
	h.saveOverridesToCollection(map[string]Binding{"cmd+x": {}})
	if !h.overridesUnreadable.Load() {
		t.Fatal("a refused save leaves the mark until a read succeeds")
	}
}
