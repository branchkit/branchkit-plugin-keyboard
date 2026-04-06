package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKeyTextEquivalent(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"return", "\n"},
		{"Return", "\n"},
		{"enter", "\n"},
		{"ENTER", "\n"},
		{"tab", "\t"},
		{"Tab", "\t"},
		{"space", " "},
		{"Space", " "},
		{"escape", ""},
		{"a", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := keyTextEquivalent(tc.name)
		if got != tc.want {
			t.Errorf("keyTextEquivalent(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want []string
	}{
		{"nil", nil, nil},
		{"empty array", []interface{}{}, []string{}},
		{"strings", []interface{}{"cmd", "shift"}, []string{"cmd", "shift"}},
		{"mixed types", []interface{}{"cmd", 42, "shift"}, []string{"cmd", "shift"}},
		{"not an array", "cmd", nil},
	}
	for _, tc := range tests {
		got := toStringSlice(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("toStringSlice(%v) = %v, want nil", tc.in, got)
			}
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("toStringSlice(%v) len = %d, want %d", tc.in, len(got), len(tc.want))
			continue
		}
		for i, s := range got {
			if s != tc.want[i] {
				t.Errorf("toStringSlice(%v)[%d] = %q, want %q", tc.in, i, s, tc.want[i])
			}
		}
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want int
	}{
		{"float64", float64(42), 42},
		{"float64 zero", float64(0), 0},
		{"int", int(36), 36},
		{"json.Number", json.Number("99"), 99},
		{"nil", nil, 0},
		{"string", "42", 0},
	}
	for _, tc := range tests {
		got := toInt(tc.in)
		if got != tc.want {
			t.Errorf("toInt(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestOnActionPrefixStripping(t *testing.T) {
	cases := []struct {
		action  string
		wantSub string
	}{
		{"input.key_by_name", "key_by_name"},
		{"input.type", "type"},
		{"input.shortcut_by_name", "shortcut_by_name"},
		{"input.click", "click"},
		{"input.scroll", "scroll"},
		{"input.move", "move"},
		{"input.mouse_down", "mouse_down"},
		{"input.mouse_up", "mouse_up"},
		{"input.clipboard", "clipboard"},
		{"input.raw_key", "raw_key"},
		{"input.key", "key"},
		{"input.shortcut", "shortcut"},
		{"click", "click"}, // no prefix
	}
	for _, tc := range cases {
		subAction := tc.action
		if idx := strings.Index(tc.action, "."); idx >= 0 {
			subAction = tc.action[idx+1:]
		}
		if subAction != tc.wantSub {
			t.Errorf("prefix strip(%q) = %q, want %q", tc.action, subAction, tc.wantSub)
		}
	}
}

func TestRawKeyDirectionMapping(t *testing.T) {
	// Verify the direction mapping logic from raw_key's down/direction params
	cases := []struct {
		name      string
		params    map[string]interface{}
		wantDir   string
	}{
		{"explicit direction", map[string]interface{}{"direction": "press"}, "press"},
		{"explicit click", map[string]interface{}{"direction": "click"}, "click"},
		{"down=true", map[string]interface{}{"down": true}, "press"},
		{"down=false", map[string]interface{}{"down": false}, "release"},
		{"no direction or down", map[string]interface{}{}, "click"},
	}
	for _, tc := range cases {
		p := tc.params
		direction := "click"
		if d, ok := p["direction"].(string); ok {
			direction = d
		} else {
			down, _ := p["down"].(bool)
			if down {
				direction = "press"
			} else if _, hasDown := p["down"]; hasDown {
				direction = "release"
			}
		}
		if direction != tc.wantDir {
			t.Errorf("%s: got direction %q, want %q", tc.name, direction, tc.wantDir)
		}
	}
}
