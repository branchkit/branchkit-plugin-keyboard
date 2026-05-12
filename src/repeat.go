package main

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

type repeatConfig struct {
	InitialDelay   time.Duration
	RepeatInterval time.Duration
}

type holdState struct {
	id     uint64
	cancel context.CancelFunc
	code   int
	mods   []string
}

var (
	activeHold *holdState
	holdSeq    atomic.Uint64
	repeatCfg  repeatConfig
)

var modifierKeyCodes = map[string]int{
	"cmd": 55, "command": 55,
	"shift": 56,
	"option": 58, "opt": 58, "alt": 58,
	"ctrl": 59, "control": 59,
	"right_cmd": 54, "right_shift": 60, "right_option": 61, "right_ctrl": 62,
	"fn": 63,
}

const safetyTimeout = 30 * time.Second

func loadRepeatConfig(p *shared.Plugin) repeatConfig {
	cfg := repeatConfig{
		InitialDelay:   500 * time.Millisecond,
		RepeatInterval: 33 * time.Millisecond,
	}

	var delay float64
	if err := p.Call("native.key_repeat_delay", nil, &delay); err == nil && delay > 0 {
		cfg.InitialDelay = time.Duration(delay * float64(time.Second))
	}

	var rate float64
	if err := p.Call("native.key_repeat_rate", nil, &rate); err == nil && rate > 0 {
		cfg.RepeatInterval = time.Duration(float64(time.Second) / rate)
	}

	shared.Logf("keyboard", "repeat config: delay=%v interval=%v", cfg.InitialDelay, cfg.RepeatInterval)
	return cfg
}

func resolveKeyCode(name string) (int, bool) {
	mu.Lock()
	defer mu.Unlock()
	code, ok := state.KeyNamesMerged[strings.ToLower(name)]
	if ok {
		return int(code), true
	}
	return 0, false
}

func pressRawKey(code int, direction string) {
	logErr("repeat.raw_key", plugin.Call("input.raw_key", map[string]any{
		"code":      code,
		"direction": direction,
	}, nil))
}

func startHold(code int, mods []string) {
	mu.Lock()
	prev := activeHold
	id := holdSeq.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	activeHold = &holdState{id: id, cancel: cancel, code: code, mods: mods}
	mu.Unlock()

	if prev != nil {
		prev.cancel()
		releaseKeys(prev.code, prev.mods)
	}

	pressModifiers(mods, "press")
	pressRawKey(code, "press")

	go runRepeatLoop(ctx, id, code)
}

func stopHold(code int, mods []string) {
	mu.Lock()
	h := activeHold
	if h != nil {
		activeHold = nil
	}
	mu.Unlock()

	if h != nil {
		h.cancel()
	}

	pressRawKey(code, "release")
	pressModifiers(mods, "release")
}

func releaseKeys(code int, mods []string) {
	pressRawKey(code, "release")
	pressModifiers(mods, "release")
}

func pressModifiers(mods []string, direction string) {
	for _, m := range mods {
		if code, ok := modifierKeyCodes[strings.ToLower(m)]; ok {
			pressRawKey(code, direction)
		}
	}
}

func runRepeatLoop(ctx context.Context, id uint64, code int) {
	timer := time.NewTimer(repeatCfg.InitialDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(repeatCfg.RepeatInterval)
	defer ticker.Stop()

	deadline := time.After(safetyTimeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			mu.Lock()
			if activeHold != nil && activeHold.id == id {
				h := activeHold
				activeHold = nil
				mu.Unlock()
				releaseKeys(h.code, h.mods)
			} else {
				mu.Unlock()
			}
			return
		case <-ticker.C:
			pressRawKey(code, "click")
		}
	}
}
