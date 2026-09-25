package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// watchPause shortens the capture timeout and counts the pause's holds and
// releases on a test host.
func watchPause(t *testing.T) (*Host, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	prev := captureTimeout
	captureTimeout = 30 * time.Millisecond
	t.Cleanup(func() { captureTimeout = prev })
	h := newTestHost()
	var holds, releases atomic.Int32
	h.holdPause = func() { holds.Add(1) }
	h.releasePause = func() { releases.Add(1) }
	return h, &holds, &releases
}

// A capture whose page went away (Settings tab closed mid-capture) must not
// hold every hotkey paused: it cancels itself and releases the pause.
func TestAnAbandonedCaptureReleasesThePause(t *testing.T) {
	h, holds, releases := watchPause(t)
	h.state.BindPicker = []bindCandidate{{ID: "c1"}}
	if err := h.handleChooseBind(&ChooseBindRequest{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	started := h.state.PendingBind != nil
	h.mu.Unlock()
	if holds.Load() != 1 || !started {
		t.Fatalf("capture did not start: holds=%d", holds.Load())
	}

	time.Sleep(150 * time.Millisecond)

	if releases.Load() != 1 {
		t.Fatalf("abandoned capture released %d times, want 1", releases.Load())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state.PendingBind != nil {
		t.Fatal("the expired capture is still pending")
	}
}

// A capture that ends normally stops its timer: the pause is released once,
// by the capture, never again by a late expiry.
func TestAFinishedCaptureIsNotReleasedTwice(t *testing.T) {
	h, _, releases := watchPause(t)
	if err := h.handleStartRemap(&StartRemapRequest{Combo: "opt+t"}); err != nil {
		t.Fatal(err)
	}
	if err := h.handleCancelRemap(nil); err != nil {
		t.Fatal(err)
	}

	time.Sleep(150 * time.Millisecond)

	if got := releases.Load(); got != 1 {
		t.Fatalf("released %d times, want 1", got)
	}
}
