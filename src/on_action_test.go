package main

import "testing"

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
