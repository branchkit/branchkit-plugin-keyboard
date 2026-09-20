package main

import (
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
	commands, err := h.plugin.CommandsEnumerate()
	if err != nil {
		return nil, err
	}
	out := make([]bindCandidate, 0, len(commands))
	for _, c := range commands {
		// `binding` is typed as *KeybindBinding on the generated command
		// (declared 2026-09-19), so the raw-JSON unmarshal this used to do
		// is gone — absence is a nil pointer rather than a zero-length blob.
		if c.Binding == nil {
			continue
		}
		b := Binding{Action: c.Binding.Action, Params: c.Binding.Params}
		if b.IsZero() {
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

func (h *Host) handleOpenBindPicker(_ *struct{}) error {
	cands, err := h.fetchBindableCommands()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.state.BindError = "Could not list commands: " + err.Error()
		return nil
	}
	h.state.BindPicker = cands
	h.state.PendingBind = nil
	return nil
}

func (h *Host) handleCloseBindPicker(_ *struct{}) error {
	h.mu.Lock()
	pending := h.state.PendingBind != nil
	h.state.BindPicker = nil
	h.state.PendingBind = nil
	h.mu.Unlock()
	if pending {
		h.resumeKeybinds()
	}
	return nil
}

func (h *Host) handleChooseBind(req *ChooseBindRequest) error {
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
	return nil
}

func (h *Host) handleCancelBind(_ *struct{}) error {
	h.mu.Lock()
	h.state.PendingBind = nil
	h.mu.Unlock()
	h.resumeKeybinds()
	return nil
}

func (h *Host) handleBindKeydown(req *BindKeydownRequest) error {
	// `input.parse_key_event` is the platform's, and this plugin's local copy
	// is gone. The copy emitted punctuation glyphs for `=`, `[` and `'` while
	// `_platform.key_names` names those keys `equals`, `leftbracket` and
	// `apostrophe` — so a binding recorded on one of them named a key nothing
	// could resolve. Key naming is platform state; this parsing follows it.
	parsed, err := h.parseKeyEvent(req.DOMKeyEvent)
	if err != nil {
		branchkit.Logf("keyboard", "bind keydown: parse failed: %v", err)
		return nil
	}

	// Escape → cancel the capture, keep the picker open.
	if parsed.IsEscape {
		return h.handleCancelBind(nil)
	}
	if parsed.IsBareModifier {
		return nil
	}
	if !parsed.HasModifiers {
		h.mu.Lock()
		h.state.BindError = "A binding needs at least one modifier key."
		h.mu.Unlock()
		return nil
	}

	h.mu.Lock()
	pending := h.state.PendingBind
	if pending == nil {
		h.mu.Unlock()
		return nil
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
	return nil
}
