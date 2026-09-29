package main

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

// keyEvents records what the hold machinery sends through the rawKey seam.
// The repeat loop sends from its own goroutine, hence the lock.
type keyEvents struct {
	mu  sync.Mutex
	evs []string
}

func (k *keyEvents) send(code int, dir string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.evs = append(k.evs, fmt.Sprintf("%d %s", code, dir))
}

func (k *keyEvents) all() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.evs...)
}

func (k *keyEvents) count(ev string) int {
	n := 0
	for _, e := range k.all() {
		if e == ev {
			n++
		}
	}
	return n
}

// newHoldHost is a test host whose raw key events are recorded, with a
// registry that resolves the modifiers pressModifiers looks up.
func newHoldHost(t *testing.T) (*Host, *keyEvents) {
	t.Helper()
	h := newTestHost()
	rec := &keyEvents{}
	h.rawKey = rec.send
	h.mu.Lock()
	h.state.KeyNamesMerged = map[string]uint16{"a": 1, "b": 2, "shift": 56, "cmd": 55}
	h.mu.Unlock()
	h.repeatCfg = repeatConfig{InitialDelay: time.Millisecond, RepeatInterval: time.Millisecond}
	t.Cleanup(func() {
		// Stop any repeat goroutine a failing test left running.
		h.mu.Lock()
		hold := h.activeHold
		h.activeHold = nil
		h.mu.Unlock()
		if hold != nil {
			hold.cancel()
		}
	})
	return h, rec
}

// waitFor polls cond until it holds or a generous deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *Host) activeHoldCode() (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeHold == nil {
		return 0, false
	}
	return h.activeHold.key.code, true
}

// A held modifier is virtual: no raw event, just bookkeeping that later key
// actions merge in. Releasing removes one instance.
func TestModifierHoldIsBookkeepingOnly(t *testing.T) {
	h, rec := newHoldHost(t)
	h.startHold(keyTarget{name: "shift", code: 56}, nil, true)
	h.startHold(keyTarget{name: "cmd", code: 55}, nil, false)
	if got := h.activeModifiers(); !reflect.DeepEqual(got, []string{"shift", "cmd"}) {
		t.Fatalf("held = %v, want [shift cmd]", got)
	}
	h.stopHold(keyTarget{name: "shift", code: 56}, nil)
	if got := h.activeModifiers(); !reflect.DeepEqual(got, []string{"cmd"}) {
		t.Fatalf("after releasing shift, held = %v, want [cmd]", got)
	}
	h.stopHold(keyTarget{name: "cmd", code: 55}, nil)
	if got := h.activeModifiers(); got != nil {
		t.Fatalf("after releasing all, held = %v, want none", got)
	}
	if evs := rec.all(); len(evs) != 0 {
		t.Fatalf("a modifier hold sent raw events: %v", evs)
	}
	if _, ok := h.activeHoldCode(); ok {
		t.Fatal("a modifier hold took the active hold slot")
	}
}

// A second hold supersedes the first: the first key is released before the
// second is pressed, so two keys are never down at once.
func TestNewHoldReleasesThePrevious(t *testing.T) {
	h, rec := newHoldHost(t)
	h.startHold(keyTarget{name: "a", code: 1}, nil, false)
	h.startHold(keyTarget{name: "b", code: 2}, nil, false)
	want := []string{"1 press", "1 release", "2 press"}
	if got := rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if code, _ := h.activeHoldCode(); code != 2 {
		t.Fatalf("active hold = %d, want 2", code)
	}
}

// A hold's modifiers go down before its key and come up after it.
func TestHoldPressesAndReleasesItsModifiers(t *testing.T) {
	h, rec := newHoldHost(t)
	h.startHold(keyTarget{name: "a", code: 1}, []string{"shift", "not-a-mod"}, false)
	h.stopHold(keyTarget{name: "a", code: 1}, []string{"shift", "not-a-mod"})
	want := []string{"56 press", "1 press", "1 release", "56 release"}
	if got := rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Two hold keybinds overlapping: down A, down B, up A. A's stop arrives
// after B superseded it. It must leave B alone — B's key is still down and
// its repeat must keep running until B's own stop.
func TestStaleStopLeavesTheActiveHold(t *testing.T) {
	h, rec := newHoldHost(t)
	a := keyTarget{name: "a", code: 1}
	b := keyTarget{name: "b", code: 2}
	h.startHold(a, []string{"shift"}, true)
	h.startHold(b, []string{"shift"}, true)
	h.stopHold(a, []string{"shift"})

	if code, ok := h.activeHoldCode(); !ok || code != 2 {
		t.Fatalf("A's stop cancelled B's hold (active=%d,%v)", code, ok)
	}
	// A was released when B superseded it; the stale stop sends nothing more,
	// and above all does not lift the shift B is holding.
	evs := rec.all()
	if n := rec.count("56 release"); n != 1 {
		t.Fatalf("shift released %d times, want once (when B superseded A): %v", n, evs)
	}
	// B still repeats.
	before := rec.count("2 click")
	waitFor(t, "B to keep repeating after A's stop", func() bool { return rec.count("2 click") >= before+3 })

	h.stopHold(b, []string{"shift"})
	if _, ok := h.activeHoldCode(); ok {
		t.Fatal("B's own stop left a hold active")
	}
	if n := rec.count("2 release"); n != 1 {
		t.Fatalf("B released %d times, want 1", n)
	}
}

// A repeating hold clicks its key on the system cadence and stops clicking
// once released.
func TestRepeatLoopClicksUntilStopped(t *testing.T) {
	h, rec := newHoldHost(t)
	k := keyTarget{name: "a", code: 1}
	h.startHold(k, nil, true)
	waitFor(t, "3 repeat clicks", func() bool { return rec.count("1 click") >= 3 })
	h.stopHold(k, nil)

	// The loop exits on cancel; after that no click follows the release.
	// Give it a few intervals to prove the negative.
	time.Sleep(20 * time.Millisecond)
	evs := rec.all()
	last := -1
	for i, e := range evs {
		if e == "1 release" {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("no release sent: %v", evs)
	}
	for _, e := range evs[last+1:] {
		if e == "1 click" {
			t.Fatalf("clicked after release: %v", evs)
		}
	}
}

// A non-repeating hold presses once and never clicks.
func TestNonRepeatingHoldDoesNotClick(t *testing.T) {
	h, rec := newHoldHost(t)
	h.startHold(keyTarget{name: "a", code: 1}, nil, false)
	time.Sleep(20 * time.Millisecond)
	if n := rec.count("1 click"); n != 0 {
		t.Fatalf("non-repeating hold clicked %d times", n)
	}
}

// A repeating hold whose stop never arrives is released by the safety
// timeout, modifiers and all.
func TestSafetyTimeoutReleasesAForgottenHold(t *testing.T) {
	h, rec := newHoldHost(t)
	h.safetyTimeout = 10 * time.Millisecond
	h.startHold(keyTarget{name: "a", code: 1}, []string{"shift"}, true)
	waitFor(t, "the safety timeout to release the key", func() bool { return rec.count("1 release") == 1 })
	waitFor(t, "the safety timeout to release shift", func() bool { return rec.count("56 release") == 1 })
	if _, ok := h.activeHoldCode(); ok {
		t.Fatal("the timed-out hold is still active")
	}
}

// The safety timeout of a superseded hold must not release its successor.
func TestSafetyTimeoutSparesANewerHold(t *testing.T) {
	h, rec := newHoldHost(t)
	h.safetyTimeout = 10 * time.Millisecond
	h.startHold(keyTarget{name: "a", code: 1}, nil, true)
	h.startHold(keyTarget{name: "b", code: 2}, nil, false) // no loop, no timeout of its own
	time.Sleep(40 * time.Millisecond)
	if code, ok := h.activeHoldCode(); !ok || code != 2 {
		t.Fatalf("A's timeout touched B (active=%d,%v)", code, ok)
	}
	if n := rec.count("2 release"); n != 0 {
		t.Fatalf("B released %d times by A's timeout", n)
	}
}

func TestMergeModifiers(t *testing.T) {
	cases := []struct {
		explicit, held, want []string
	}{
		{nil, nil, nil},
		{[]string{"cmd"}, nil, []string{"cmd"}},
		{nil, []string{"shift"}, []string{"shift"}},
		{[]string{"cmd"}, []string{"shift"}, []string{"cmd", "shift"}},
		{[]string{"Shift"}, []string{"shift", "opt"}, []string{"Shift", "opt"}}, // case-insensitive dedupe
	}
	for _, c := range cases {
		got := mergeModifiers(c.explicit, c.held)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("mergeModifiers(%v, %v) = %v, want %v", c.explicit, c.held, got, c.want)
		}
	}
	// The explicit slice is not aliased by the merge.
	explicit := make([]string, 1, 4)
	explicit[0] = "cmd"
	_ = mergeModifiers(explicit, []string{"shift"})
	if explicit[:2][1] == "shift" {
		t.Error("mergeModifiers wrote into the caller's backing array")
	}
}

func TestHoldPhase(t *testing.T) {
	ph := func(s string) *branchkit.OnActionRequest { return &branchkit.OnActionRequest{Phase: &s} }
	cases := map[string]*branchkit.OnActionRequest{
		"":       {},
		"start":  ph("start"),
		"repeat": ph("repeat"),
		"stop":   ph("stop"),
	}
	for want, req := range cases {
		if got := holdPhase(req); got != want {
			t.Errorf("holdPhase(%v) = %q, want %q", req.Phase, got, want)
		}
	}
	for _, other := range []string{"", "down", "STOP"} {
		if got := holdPhase(ph(other)); got != "" {
			t.Errorf("holdPhase(%q) = %q, want empty", other, got)
		}
	}
}

func TestButtonOrLeft(t *testing.T) {
	empty, right := ClickButton(""), ClickButtonRight
	for _, c := range []struct {
		in   *ClickButton
		want string
	}{{nil, "left"}, {&empty, "left"}, {&right, "right"}} {
		if got := buttonOrLeft(c.in); got != c.want {
			t.Errorf("buttonOrLeft(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// isPrintable's doc also promises false for whitespace-only strings; the code
// accepts " " (0x20). That disagreement is left to the owner, so a lone space
// is deliberately not pinned here.
func TestIsPrintable(t *testing.T) {
	for in, want := range map[string]bool{
		"":      false,
		"a":     true,
		"é":     true,
		"\t":    false,
		"a\nb":  false,
		"\x7f":  false,
		"\x1b[": false,
	} {
		if got := isPrintable(in); got != want {
			t.Errorf("isPrintable(%q) = %v, want %v", in, got, want)
		}
	}
}
