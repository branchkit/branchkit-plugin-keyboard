package main

import (
	"fmt"

	branchkit "github.com/branchkit/plugin-sdk-go"
)

// DOMKeyEvent is the browser `KeyboardEvent` shape a settings-tab capture
// sends up. It is the request body for `input.parse_key_event`.
type DOMKeyEvent struct {
	Code     string `json:"code"`
	Key      string `json:"key"`
	CtrlKey  bool   `json:"ctrl"`
	AltKey   bool   `json:"alt"`
	ShiftKey bool   `json:"shift"`
	MetaKey  bool   `json:"meta"`
}

// ParsedKeyEvent is the platform's response type, not a local restatement of
// it. A hand-written mirror of a platform shape zero-fills silently when the
// platform renames a field; this breaks the build instead.
type ParsedKeyEvent = branchkit.InputParseKeyEventResponse

// parseKeyEvent asks the platform to turn a browser key event into a combo.
//
// The table, the registry guard and the numpad spellings this file used to
// carry are all in `input.parse_key_event` now. They were fixed twice in
// parallel — once here in 3ec17b9, once in the platform — because voice needed
// the same parse and reached for `plugin.keyboard.parse_key_event`, which the
// actuator does not route. One copy, on the side that owns the key registry,
// is what stops the third divergence.
//
// A package-level var, not a plain call, for one reason: the parse used to be
// local and pure, so the handlers that use it were unit-testable with no
// running host. Reaching a platform operation needs a live RPC, and that is a
// real cost of centralising. Tests swap this the same way they already swap
// `fetchBindableCommands`.
var parseKeyEvent = func(ev DOMKeyEvent) (ParsedKeyEvent, error) {
	var parsed ParsedKeyEvent
	if plugin == nil {
		return parsed, fmt.Errorf("no plugin connection")
	}
	return parsed, plugin.Call("input.parse_key_event", ev, &parsed)
}
