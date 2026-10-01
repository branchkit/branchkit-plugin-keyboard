package main

import (
	"encoding/json"
	"sort"

	"github.com/branchkit/plugin-sdk-go"
)

// The user's edits live in the platform's `_platform.binding_overrides`, one
// record per edited trigger, keyed by the trigger. The platform re-derives
// the hotkey table on every write there, so saving an edit is what applies
// it — there is nothing to register afterwards.
const overridesCollection = "_platform.binding_overrides"

// legacyOverridesCollection held every edit as one whole-map singleton until
// 2026-09-30. Its records are carried over once at start (migrateLegacyOverrides).
const legacyOverridesCollection = "plugin.keyboard.overrides"

type overrideRecord struct {
	ID      string          `json:"id"`
	Source  string          `json:"source"`
	Trigger string          `json:"trigger"`
	Action  string          `json:"action"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (h *Host) loadOverridesFromCollection() map[string]Binding {
	recs, err := h.plugin.ListAll(overridesCollection)
	if err != nil {
		branchkit.Logf("keyboard", "overrides read error: %v", err)
		h.overridesUnreadable.Store(true)
		return make(map[string]Binding)
	}
	h.overridesUnreadable.Store(false)
	out := make(map[string]Binding, len(recs))
	for _, r := range recs {
		var o overrideRecord
		if err := json.Unmarshal(r.Payload, &o); err != nil || o.Trigger == "" {
			branchkit.Logf("keyboard", "override %q unreadable, skipped: %v", r.ID, err)
			continue
		}
		out[o.Trigger] = Binding{Action: o.Action, Params: nonNullParams(o.Params)}
	}
	return out
}

// saveOverridesToCollection makes the stored edits equal `overrides`: one put
// per new or changed trigger, one delete per trigger no longer in it.
func (h *Host) saveOverridesToCollection(overrides map[string]Binding) {
	if h.overridesUnreadable.Load() {
		// The map was built without the saved edits, so making the store
		// equal to it would delete them.
		branchkit.Logf("keyboard", "not saving overrides: the saved ones could not be read, and "+
			"this map was built without them")
		return
	}
	current := h.loadOverridesFromCollection()
	if h.overridesUnreadable.Load() {
		branchkit.Logf("keyboard", "not saving overrides: could not read what is stored")
		return
	}
	triggers := make([]string, 0, len(overrides))
	for t := range overrides {
		triggers = append(triggers, t)
	}
	sort.Strings(triggers)
	for _, t := range triggers {
		b := overrides[t]
		if prev, ok := current[t]; ok && prev.Action == b.Action && string(prev.Params) == string(b.Params) {
			continue
		}
		rec := overrideRecord{ID: t, Source: "keyboard", Trigger: t, Action: b.Action, Params: b.Params}
		if err := h.plugin.Put(overridesCollection, t, rec); err != nil {
			branchkit.Logf("keyboard", "failed to save override %q: %v", t, err)
		}
	}
	for t := range current {
		if _, keep := overrides[t]; keep {
			continue
		}
		if _, err := h.plugin.Delete(overridesCollection, t); err != nil {
			branchkit.Logf("keyboard", "failed to remove override %q: %v", t, err)
		}
	}
}

// migrateLegacyOverrides carries edits saved in the old whole-map singleton
// into the platform's per-trigger records, then removes the singleton. An
// edit already in the new store wins: it is the newer one.
func (h *Host) migrateLegacyOverrides() {
	rec, err := h.plugin.Get(legacyOverridesCollection, "singleton")
	if err != nil || rec == nil {
		return
	}
	var legacy map[string]Binding
	if err := json.Unmarshal(rec.Payload, &legacy); err != nil {
		branchkit.Logf("keyboard", "legacy overrides unreadable, left in place: %v", err)
		return
	}
	merged := h.loadOverridesFromCollection()
	if h.overridesUnreadable.Load() {
		return
	}
	for t, b := range legacy {
		if _, newer := merged[t]; !newer {
			merged[t] = b
		}
	}
	h.saveOverridesToCollection(merged)
	if _, err := h.plugin.Delete(legacyOverridesCollection, "singleton"); err != nil {
		branchkit.Logf("keyboard", "legacy overrides carried over but not removed: %v", err)
		return
	}
	branchkit.Logf("keyboard", "carried %d saved keybind edits over to %s", len(legacy), overridesCollection)
}
