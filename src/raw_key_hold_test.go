package main

import (
	"reflect"
	"testing"

	branchkit "github.com/branchkit/plugin-sdk-go"
)

// input.raw_key bound to a held trigger follows the trigger's edges: down
// on start, up on stop, and a repeat binding clicks until its stop. It used
// to ignore the phase and send its one-shot event on both edges: a click on
// press and another on release, or with `direction: press` a second press
// and never a release.
func TestRawKeyFollowsTheHoldPhase(t *testing.T) {
	ph := func(s string) *branchkit.OnActionRequest { return &branchkit.OnActionRequest{Phase: &s} }
	press := RawKeyDirection("press")

	h, rec := newHoldHost(t)
	// The params' own direction is ignored under a phase.
	p := RawKeyParams{Code: 7, Direction: &press}
	if _, err := h.handleInputRawKey(p, ph("start")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.handleInputRawKey(p, ph("stop")); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.all(), []string{"7 press", "7 release"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hold events = %v, want %v", got, want)
	}

	h, rec = newHoldHost(t)
	if _, err := h.handleInputRawKey(RawKeyParams{Code: 7}, ph("repeat")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "3 repeat clicks", func() bool { return rec.count("7 click") >= 3 })
	if _, err := h.handleInputRawKey(RawKeyParams{Code: 7}, ph("stop")); err != nil {
		t.Fatal(err)
	}
	if n := rec.count("7 release"); n != 1 {
		t.Fatalf("released %d times after a repeat, want 1: %v", n, rec.all())
	}
	if _, ok := h.activeHoldCode(); ok {
		t.Fatal("the stop left a hold active")
	}
}
