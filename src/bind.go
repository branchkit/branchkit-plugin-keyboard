package main

import (
	"encoding/json"
	"sort"

	branchkit "github.com/branchkit/plugin-sdk-go"
)

// A command offered by the bind-a-command picker: display identity plus the
// ready-to-store binding value the platform computed (commands.enumerate's
// `binding` field — present only for statically bindable actions, so the
// picker never offers a capture template, a sequence, or a phased action).
type bindCandidate struct {
	ID      string
	Pattern string
	Owner   string
	B       Binding
}

// Var seam so handler tests can run the real picker flow without a live
// actuator behind plugin.Call.
var fetchBindableCommands = func() ([]bindCandidate, error) {
	var resp struct {
		Commands []struct {
			ID          string          `json:"id"`
			OwnerPlugin string          `json:"owner_plugin"`
			Pattern     string          `json:"pattern"`
			Binding     json.RawMessage `json:"binding"`
		} `json:"commands"`
	}
	if err := plugin.Call("commands.enumerate", struct{}{}, &resp); err != nil {
		return nil, err
	}
	out := make([]bindCandidate, 0, len(resp.Commands))
	for _, c := range resp.Commands {
		if len(c.Binding) == 0 {
			continue
		}
		var b Binding
		if err := json.Unmarshal(c.Binding, &b); err != nil || b.IsZero() {
			continue
		}
		out = append(out, bindCandidate{ID: c.ID, Pattern: c.Pattern, Owner: c.OwnerPlugin, B: b})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out, nil
}

type ChooseBindRequest struct {
	ID string `json:"id"`
}

// BindKeydownRequest accepts raw DOM key event properties while a bind
// capture is open — the same parsing the remap capture uses.
type BindKeydownRequest struct {
	DOMKeyEvent
}

func handleOpenBindPicker(_ *struct{}) (any, error) {
	cands, err := fetchBindableCommands()
	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		state.BindError = "Could not list commands: " + err.Error()
		return OkResponse{OK: false}, nil
	}
	state.BindPicker = cands
	state.PendingBind = nil
	return OkResponse{OK: true}, nil
}

func handleCloseBindPicker(_ *struct{}) (any, error) {
	mu.Lock()
	pending := state.PendingBind != nil
	state.BindPicker = nil
	state.PendingBind = nil
	mu.Unlock()
	if pending {
		resumeKeybinds()
	}
	return OkResponse{OK: true}, nil
}

func handleChooseBind(req *ChooseBindRequest) (any, error) {
	mu.Lock()
	for i := range state.BindPicker {
		if state.BindPicker[i].ID == req.ID {
			c := state.BindPicker[i]
			state.PendingBind = &c
			break
		}
	}
	found := state.PendingBind != nil
	mu.Unlock()
	if found {
		pauseKeybinds()
	}
	return OkResponse{OK: found}, nil
}

func handleCancelBind(_ *struct{}) (any, error) {
	mu.Lock()
	state.PendingBind = nil
	mu.Unlock()
	resumeKeybinds()
	return OkResponse{OK: true}, nil
}

func handleBindKeydown(req *BindKeydownRequest) (any, error) {
	// `input.parse_key_event` is the platform's, and this plugin's local copy
	// is gone. The copy emitted punctuation glyphs for `=`, `[` and `'` while
	// `_platform.key_names` names those keys `equals`, `leftbracket` and
	// `apostrophe` — so a binding recorded on one of them named a key nothing
	// could resolve. Key naming is platform state; this parsing follows it.
	parsed, err := parseKeyEvent(req.DOMKeyEvent)
	if err != nil {
		branchkit.Logf("keyboard", "bind keydown: parse failed: %v", err)
		return OkResponse{OK: false}, nil
	}

	// Escape → cancel the capture, keep the picker open.
	if parsed.IsEscape {
		return handleCancelBind(nil)
	}
	if parsed.IsBareModifier {
		return OkResponse{OK: true}, nil
	}
	if !parsed.HasModifiers {
		mu.Lock()
		state.BindError = "A binding needs at least one modifier key."
		mu.Unlock()
		return OkResponse{OK: false}, nil
	}

	mu.Lock()
	pending := state.PendingBind
	if pending == nil {
		mu.Unlock()
		return OkResponse{OK: false}, nil
	}
	// The binding is a user override: it wins over any plugin bind on the
	// same combo, exactly as a remap does, and Reset removes it.
	overrides := loadUserKeybindOverrides()
	overrides[parsed.Combo] = pending.B
	saveUserKeybindOverrides(overrides)
	state.PendingBind = nil
	state.BindPicker = nil
	snapshot := state.rebuild()
	mu.Unlock()
	// Outside the lock: registration is an RPC (same rule as handleRemap).
	registerKeybinds(snapshot)
	resumeKeybinds()
	return snapshot, nil
}
