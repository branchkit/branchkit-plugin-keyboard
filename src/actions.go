package main

import (
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

// --- on_action: input simulation via native API ---
//
// Each input.* action gets its own typed handler, registered in main() via the
// Handle<Action> registrars generated from plugin.json into actions_gen.go.
// Params arrive already unmarshaled; the action string is never spelled here.

func logErr(action string, err error) {
	if err != nil {
		branchkit.Logf("keyboard", "%s: %v", action, err)
	}
}

func holdPhase(req *branchkit.OnActionRequest) string {
	if req.Phase == nil {
		return ""
	}
	switch *req.Phase {
	case "start", "repeat", "stop":
		return *req.Phase
	default:
		return ""
	}
}

// Param structs (TypeParams, KeyByNameParams, …) live in actions_gen.go,
// generated from plugin.json's action_types block. Edit that and re-run
// `just gen-plugins` — do not hand-declare these structs here.

func mergeModifiers(explicit []string, held []string) []string {
	if len(held) == 0 {
		return explicit
	}
	seen := make(map[string]bool, len(explicit))
	for _, m := range explicit {
		seen[strings.ToLower(m)] = true
	}
	merged := make([]string, len(explicit))
	copy(merged, explicit)
	for _, m := range held {
		if !seen[strings.ToLower(m)] {
			merged = append(merged, m)
		}
	}
	return merged
}

func buttonOrLeft(b *ClickButton) string {
	if b == nil || *b == "" {
		return "left"
	}
	return string(*b)
}

func (h *Host) handleInputType(p TypeParams, req *branchkit.OnActionRequest) (any, error) {
	if p.Text == "" {
		return nil, nil
	}
	logErr("input.type", h.plugin.InputTypeText(p.Text))
	return nil, nil
}

func (h *Host) handleInputKeyByName(p KeyByNameParams, req *branchkit.OnActionRequest) (any, error) {
	if phase := holdPhase(req); phase != "" {
		code, ok := h.resolveKeyCode(p.Name)
		if !ok {
			return nil, nil
		}
		// Carry the NAME as well as the code: modifier classification has to
		// be by name, since the registry is per-OS.
		t := keyTarget{name: p.Name, code: code}
		switch phase {
		case "start":
			h.startHold(t, p.Modifiers, false)
		case "repeat":
			h.startHold(t, p.Modifiers, true)
		default:
			h.stopHold(t, p.Modifiers)
		}
		return nil, nil
	}
	// "text" strategy: paste text equivalent instead of key event (when no modifiers)
	if p.Strategy != nil && *p.Strategy == KeyByNameStrategyText && len(p.Modifiers) == 0 {
		if textEquiv := keyTextEquivalent(p.Name); textEquiv != "" {
			logErr("input.key_by_name", h.plugin.InputTypeText(textEquiv))
			return nil, nil
		}
	}
	mods := mergeModifiers(p.Modifiers, h.activeModifiers())
	logErr("input.key_by_name", h.plugin.InputPressKey(nil, mods, &p.Name))
	return nil, nil
}

func (h *Host) handleInputKey(p KeyParams, req *branchkit.OnActionRequest) (any, error) {
	if phase := holdPhase(req); phase != "" {
		switch phase {
		case "start":
			h.startHold(keyTarget{code: p.Code}, nil, false)
		case "repeat":
			h.startHold(keyTarget{code: p.Code}, nil, true)
		default:
			h.stopHold(keyTarget{code: p.Code}, nil)
		}
		return nil, nil
	}
	logErr("input.key", h.plugin.InputPressKey(&p.Code, h.activeModifiers(), nil))
	return nil, nil
}

func (h *Host) handleInputShortcutByName(p ShortcutByNameParams, req *branchkit.OnActionRequest) (any, error) {
	if phase := holdPhase(req); phase != "" {
		code, ok := h.resolveKeyCode(p.Name)
		if !ok {
			return nil, nil
		}
		// Carry the NAME as well as the code: modifier classification has to
		// be by name, since the registry is per-OS.
		t := keyTarget{name: p.Name, code: code}
		switch phase {
		case "start":
			h.startHold(t, p.Modifiers, false)
		case "repeat":
			h.startHold(t, p.Modifiers, true)
		default:
			h.stopHold(t, p.Modifiers)
		}
		return nil, nil
	}
	mods := mergeModifiers(p.Modifiers, h.activeModifiers())
	logErr("input.shortcut_by_name", h.plugin.InputPressKey(nil, mods, &p.Name))
	return nil, nil
}

func (h *Host) handleInputShortcut(p ShortcutParams, req *branchkit.OnActionRequest) (any, error) {
	if phase := holdPhase(req); phase != "" {
		switch phase {
		case "start":
			h.startHold(keyTarget{code: p.Code}, p.Modifiers, false)
		case "repeat":
			h.startHold(keyTarget{code: p.Code}, p.Modifiers, true)
		default:
			h.stopHold(keyTarget{code: p.Code}, p.Modifiers)
		}
		return nil, nil
	}
	mods := mergeModifiers(p.Modifiers, h.activeModifiers())
	logErr("input.shortcut", h.plugin.InputPressKey(&p.Code, mods, nil))
	return nil, nil
}

func (h *Host) handleInputRawKey(p RawKeyParams, req *branchkit.OnActionRequest) (any, error) {
	direction := "click"
	switch {
	case p.Direction != nil:
		direction = string(*p.Direction)
	case p.Down != nil && *p.Down:
		direction = "press"
	case p.Down != nil:
		direction = "release"
	}
	logErr("input.raw_key", h.plugin.InputRawKey(p.Code, direction))
	return nil, nil
}

func (h *Host) handleInputClick(p ClickParams, req *branchkit.OnActionRequest) (any, error) {
	button := buttonOrLeft(p.Button)
	logErr("input.click", h.plugin.InputClick(&button))
	return nil, nil
}

func (h *Host) handleInputScroll(p ScrollParams, req *branchkit.OnActionRequest) (any, error) {
	var unit *string
	if p.Unit != nil {
		u := string(*p.Unit)
		unit = &u
	}
	logErr("input.scroll", h.plugin.InputScroll(string(p.Direction), p.Amount, unit))
	return nil, nil
}

func (h *Host) handleInputMove(p MoveParams, req *branchkit.OnActionRequest) (any, error) {
	logErr("input.move", h.plugin.NativeWarpCursor(p.X, p.Y))
	return nil, nil
}

func (h *Host) handleInputMouseDown(p MouseDownParams, req *branchkit.OnActionRequest) (any, error) {
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_down", h.plugin.InputMouseButton("press", &button))
	return nil, nil
}

func (h *Host) handleInputMouseUp(p MouseUpParams, req *branchkit.OnActionRequest) (any, error) {
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_up", h.plugin.InputMouseButton("release", &button))
	return nil, nil
}

func (h *Host) handleInputClipboard(p ClipboardParams, req *branchkit.OnActionRequest) (any, error) {
	var text *string
	if p.Text != nil && *p.Text != "" {
		text = p.Text
	}
	logErr("input.clipboard", h.plugin.InputClipboardAction(string(p.Action), text))
	return nil, nil
}

// keyTextEquivalent returns the text equivalent of a key name for the "text" strategy.
func keyTextEquivalent(name string) string {
	switch strings.ToLower(name) {
	case "return", "enter":
		return "\n"
	case "tab":
		return "\t"
	case "space":
		return " "
	default:
		return ""
	}
}
