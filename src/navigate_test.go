package main

import "testing"

// Each destination is Command and an arrow on macOS, Home or End (Control
// for the document) on Linux and Windows.
func TestNavigateChordPerOS(t *testing.T) {
	cases := []struct {
		to   NavigateTo
		goos string
		name string
		mods []string
	}{
		{NavigateToDocumentStart, "darwin", "up", []string{"cmd"}},
		{NavigateToDocumentEnd, "darwin", "down", []string{"cmd"}},
		{NavigateToLineStart, "darwin", "left", []string{"cmd"}},
		{NavigateToLineEnd, "darwin", "right", []string{"cmd"}},
		{NavigateToDocumentStart, "linux", "home", []string{"ctrl"}},
		{NavigateToDocumentEnd, "windows", "end", []string{"ctrl"}},
		{NavigateToLineStart, "windows", "home", nil},
		{NavigateToLineEnd, "linux", "end", nil},
	}
	for _, c := range cases {
		name, mods, ok := navigateChord(c.to, c.goos)
		if !ok || name != c.name || len(mods) != len(c.mods) || (len(mods) > 0 && mods[0] != c.mods[0]) {
			t.Errorf("%s on %s: got %q %v, want %q %v", c.to, c.goos, name, mods, c.name, c.mods)
		}
	}
	if _, _, ok := navigateChord("sideways", "linux"); ok {
		t.Error("an unknown destination must not resolve")
	}
}
