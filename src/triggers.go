package main

import (
	"encoding/json"
	"sort"

	"github.com/branchkit/plugin-sdk-go"
)

// The Actions page shows an action's triggers beside its phrases, and a key
// combo is one. The platform owns the collection (`_platform.triggers`) and
// the chip forms; this plugin supplies the data — one record per binding
// that will fire, replaced as a set after every registry rebuild so the
// records are exactly the live bindings (DESIGN_PLATFORM_SETTINGS_PAGES.md,
// "Decided: dispatcher trigger declarations").
const triggersCollection = "_platform.triggers"

type triggerRecord struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
	Label  string          `json:"label"`
	Form   string          `json:"form"`
	Home   string          `json:"home"`
}

// triggerEntries is the pure half: the registry's bindings as trigger
// records, sorted by id so a replace is stable. A plain binding fires on
// press and a hold on down; the up half of a hold is the same trigger
// letting go, not a second one, so it is skipped. The label is the combo as
// the Keybinds tab spells it ("opt+shift+h", "opt+shift+h (hold)"); the
// platform's keycap form splits it into keys. `home` names the tab the
// trigger is edited on.
func triggerEntries(reg InternalRegistry) []branchkit.CollectionPutEntry {
	out := make([]branchkit.CollectionPutEntry, 0, len(reg.Entries))
	for _, e := range reg.Entries {
		if e.Combo.Event == KeyEventUp || e.Action == "" {
			continue
		}
		combo := comboBaseString(e.Combo)
		label := combo
		if e.Combo.Event == KeyEventDown {
			label += " (hold)"
		}
		rec := triggerRecord{
			ID:     "keyboard:" + combo,
			Action: e.Action,
			Params: e.Params,
			Label:  label,
			Form:   "keycap",
			Home:   "keybinds",
		}
		payload, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		out = append(out, branchkit.CollectionPutEntry{ID: rec.ID, Payload: payload})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// syncTriggers replaces this plugin's records in `_platform.triggers` with
// the registry's live bindings. Runs off the calling goroutine: rebuild()
// runs inside RPC handlers, and a nested round trip there would block the
// reply. Scoped to this plugin's own records — other dispatchers' triggers
// are untouched.
func (h *Host) syncTriggers(reg InternalRegistry) {
	entries := triggerEntries(reg)
	corr := h.plugin.CurrentCorrelation()
	go branchkit.RunWithCorrelation(corr, func() {
		if _, err := h.plugin.Replace(triggersCollection, entries, branchkit.ScopeCollection()); err != nil {
			branchkit.Logf("keyboard", "trigger declarations: %v", err)
		}
	})
}
