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
func (h *Host) fetchBindableCommandsDefault() ([]bindCandidate, error) {
	var resp struct {
		Commands []struct {
			ID          string          `json:"id"`
			OwnerPlugin string          `json:"owner_plugin"`
			Pattern     string          `json:"pattern"`
			Binding     json.RawMessage `json:"binding"`
		} `json:"commands"`
	}
	if err := h.plugin.Call("commands.enumerate", struct{}{}, &resp); err != nil {
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

func (h *Host) handleOpenBindPicker(_ *struct{}) (any, error) {
	cands, err := h.fetchBindableCommands()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.state.BindError = "Could not list commands: " + err.Error()
		return nil, nil
	}
	h.state.BindPicker = cands
	h.state.PendingBind = nil
	return nil, nil
}

func (h *Host) handleCloseBindPicker(_ *struct{}) (any, error) {
	h.mu.Lock()
	pending := h.state.PendingBind != nil
	h.state.BindPicker = nil
	h.state.PendingBind = nil
	h.mu.Unlock()
	if pending {
		h.resumeKeybinds()
	}
	return nil, nil
}

func (h *Host) handleChooseBind(req *ChooseBindRequest) (any, error) {
	h.mu.Lock()
	for i := range h.state.BindPicker {
		if h.state.BindPicker[i].ID == req.ID {
			c := h.state.BindPicker[i]
			h.state.PendingBind = &c
			break
		}
	}
	found := h.state.PendingBind != nil
	h.mu.Unlock()
	if found {
		h.pauseKeybinds()
	}
	return nil, nil
}

func (h *Host) handleCancelBind(_ *struct{}) (any, error) {
	h.mu.Lock()
	h.state.PendingBind = nil
	h.mu.Unlock()
	h.resumeKeybinds()
	return nil, nil
}

func (h *Host) handleBindKeydown(req *BindKeydownRequest) (any, error) {
	// `input.parse_key_event` is the platform's, and this plugin's local copy
	// is gone. The copy emitted punctuation glyphs for `=`, `[` and `'` while
	// `_platform.key_names` names those keys `equals`, `leftbracket` and
	// `apostrophe` — so a binding recorded on one of them named a key nothing
	// could resolve. Key naming is platform state; this parsing follows it.
	parsed, err := h.parseKeyEvent(req.DOMKeyEvent)
	if err != nil {
		branchkit.Logf("keyboard", "bind keydown: parse failed: %v", err)
		return nil, nil
	}

	// Escape → cancel the capture, keep the picker open.
	if parsed.IsEscape {
		return h.handleCancelBind(nil)
	}
	if parsed.IsBareModifier {
		return nil, nil
	}
	if !parsed.HasModifiers {
		h.mu.Lock()
		h.state.BindError = "A binding needs at least one modifier key."
		h.mu.Unlock()
		return nil, nil
	}

	h.mu.Lock()
	pending := h.state.PendingBind
	if pending == nil {
		h.mu.Unlock()
		return nil, nil
	}
	// The binding is a user override: it wins over any plugin bind on the
	// same combo, exactly as a remap does, and Reset removes it.
	overrides := h.loadUserKeybindOverrides()
	overrides[parsed.Combo] = pending.B
	h.saveUserKeybindOverrides(overrides)
	h.state.PendingBind = nil
	h.state.BindPicker = nil
	snapshot := h.state.rebuild(h)
	h.mu.Unlock()
	// Outside the lock: registration is an RPC (same rule as handleRemap).
	h.registerKeybinds(snapshot)
	h.resumeKeybinds()
	return nil, nil
}
