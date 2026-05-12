package main

import (
	"strings"

	"github.com/branchkit/plugin-sdk-go"
)

// --- on_action: input simulation via native API ---
//
// Each input.* action gets its own typed handler. The SDK demuxes by
// req.Action via plugin.HandleAction(...) — see main().

func logErr(action string, err error) {
	if err != nil {
		shared.Logf("keyboard", "%s: %v", action, err)
	}
}

func holdPhase(req *shared.OnActionRequest) string {
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

func handleInputType(req *shared.OnActionRequest) (any, error) {
	var p TypeParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if p.Text == "" {
		return nil, nil
	}
	logErr("input.type", plugin.Call("input.type_text", map[string]any{"text": p.Text}, nil))
	return nil, nil
}

func handleInputKeyByName(req *shared.OnActionRequest) (any, error) {
	var p KeyByNameParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if phase := holdPhase(req); phase != "" {
		code, ok := resolveKeyCode(p.Name)
		if !ok {
			return nil, nil
		}
		switch phase {
		case "start":
			startHold(code, p.Modifiers, false)
		case "repeat":
			startHold(code, p.Modifiers, true)
		default:
			stopHold(code, p.Modifiers)
		}
		return nil, nil
	}
	// "text" strategy: paste text equivalent instead of key event (when no modifiers)
	if p.Strategy != nil && *p.Strategy == KeyByNameStrategyText && len(p.Modifiers) == 0 {
		if textEquiv := keyTextEquivalent(p.Name); textEquiv != "" {
			logErr("input.key_by_name", plugin.Call("input.type_text", map[string]any{"text": textEquiv}, nil))
			return nil, nil
		}
	}
	mods := mergeModifiers(p.Modifiers, activeModifiers())
	params := map[string]any{"name": p.Name}
	if len(mods) > 0 {
		params["modifiers"] = mods
	}
	logErr("input.key_by_name", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputKey(req *shared.OnActionRequest) (any, error) {
	var p KeyParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if phase := holdPhase(req); phase != "" {
		switch phase {
		case "start":
			startHold(p.Code, nil, false)
		case "repeat":
			startHold(p.Code, nil, true)
		default:
			stopHold(p.Code, nil)
		}
		return nil, nil
	}
	keyParams := map[string]any{"code": p.Code}
	if held := activeModifiers(); len(held) > 0 {
		keyParams["modifiers"] = held
	}
	logErr("input.key", plugin.Call("input.press_key", keyParams, nil))
	return nil, nil
}

func handleInputShortcutByName(req *shared.OnActionRequest) (any, error) {
	var p ShortcutByNameParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if phase := holdPhase(req); phase != "" {
		code, ok := resolveKeyCode(p.Name)
		if !ok {
			return nil, nil
		}
		switch phase {
		case "start":
			startHold(code, p.Modifiers, false)
		case "repeat":
			startHold(code, p.Modifiers, true)
		default:
			stopHold(code, p.Modifiers)
		}
		return nil, nil
	}
	mods := mergeModifiers(p.Modifiers, activeModifiers())
	params := map[string]any{"name": p.Name}
	if len(mods) > 0 {
		params["modifiers"] = mods
	}
	logErr("input.shortcut_by_name", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputShortcut(req *shared.OnActionRequest) (any, error) {
	var p ShortcutParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if phase := holdPhase(req); phase != "" {
		switch phase {
		case "start":
			startHold(p.Code, p.Modifiers, false)
		case "repeat":
			startHold(p.Code, p.Modifiers, true)
		default:
			stopHold(p.Code, p.Modifiers)
		}
		return nil, nil
	}
	mods := mergeModifiers(p.Modifiers, activeModifiers())
	params := map[string]any{"code": p.Code}
	if len(mods) > 0 {
		params["modifiers"] = mods
	}
	logErr("input.shortcut", plugin.Call("input.press_key", params, nil))
	return nil, nil
}

func handleInputRawKey(req *shared.OnActionRequest) (any, error) {
	var p RawKeyParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	direction := "click"
	switch {
	case p.Direction != nil:
		direction = string(*p.Direction)
	case p.Down != nil && *p.Down:
		direction = "press"
	case p.Down != nil:
		direction = "release"
	}
	logErr("input.raw_key", plugin.Call("input.raw_key", map[string]any{"code": p.Code, "direction": direction}, nil))
	return nil, nil
}

func handleInputClick(req *shared.OnActionRequest) (any, error) {
	var p ClickParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	logErr("input.click", plugin.Call("input.click", map[string]any{"button": buttonOrLeft(p.Button)}, nil))
	return nil, nil
}

func handleInputScroll(req *shared.OnActionRequest) (any, error) {
	var p ScrollParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"direction": string(p.Direction)}
	if p.Unit != nil {
		params["unit"] = string(*p.Unit)
	}
	if p.Amount != nil {
		params["amount"] = *p.Amount
	}
	logErr("input.scroll", plugin.Call("input.scroll", params, nil))
	return nil, nil
}

func handleInputMove(req *shared.OnActionRequest) (any, error) {
	var p MoveParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	logErr("input.move", plugin.Call("native.warp_cursor", map[string]any{"x": p.X, "y": p.Y}, nil))
	return nil, nil
}

func handleInputMouseDown(req *shared.OnActionRequest) (any, error) {
	var p MouseDownParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_down", plugin.Call("input.mouse_button", map[string]any{"button": button, "direction": "press"}, nil))
	return nil, nil
}

func handleInputMouseUp(req *shared.OnActionRequest) (any, error) {
	var p MouseUpParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	button := "left"
	if p.Button != nil && *p.Button != "" {
		button = string(*p.Button)
	}
	logErr("input.mouse_up", plugin.Call("input.mouse_button", map[string]any{"button": button, "direction": "release"}, nil))
	return nil, nil
}

func handleInputClipboard(req *shared.OnActionRequest) (any, error) {
	var p ClipboardParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	params := map[string]any{"action": string(p.Action)}
	if p.Text != nil && *p.Text != "" {
		params["text"] = *p.Text
	}
	logErr("input.clipboard", plugin.Call("input.clipboard_action", params, nil))
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
