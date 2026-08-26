package main

import (
	"encoding/json"
	"html/template"
	"sort"
	"strings"

	"github.com/a-h/templ"
	toolkit "github.com/branchkit/plugin-toolkit-go"
)

type keybindRowView struct {
	ComboDisplay string
	ComboKey     string
	ComboKeyJSON template.JS // JSON-escaped for safe use in Datastar expressions
	ActionLabel  string
	BadgeClass   string
	SourceLabel  string
	IsOverride   bool
	IsHoldJSON   template.JS // "true" or "false" for safe JS embedding
}

type keybindGroupView struct {
	SourceName string
	Rows       []keybindRowView
}

func renderSettings(ps *PluginState, search string) string {
	// Group entries by (key, modifiers) ignoring event type
	type comboGroupKey struct {
		Key  string
		Mods Modifiers
	}
	type comboGroupEntry struct {
		Combo KeyCombo
		Entry KeybindEntry
	}
	comboGroups := make(map[comboGroupKey][]comboGroupEntry)
	for _, e := range ps.Registry.Entries {
		gk := comboGroupKey{Key: e.Combo.Key, Mods: e.Combo.Modifiers}
		comboGroups[gk] = append(comboGroups[gk], comboGroupEntry{Combo: e.Combo, Entry: e})
	}

	rowsBySource := make(map[string][]keybindRowView)
	hasOverrides := false

	for _, entries := range comboGroups {
		hasDown := false
		hasUp := false
		for _, e := range entries {
			if e.Combo.Event == KeyEventDown {
				hasDown = true
			}
			if e.Combo.Event == KeyEventUp {
				hasUp = true
			}
		}
		isHold := hasDown && hasUp

		if isHold {
			// Find the down entry
			var downEntry *comboGroupEntry
			for i := range entries {
				if entries[i].Combo.Event == KeyEventDown {
					downEntry = &entries[i]
					break
				}
			}
			if downEntry == nil {
				continue
			}

			comboDisplay := comboBaseString(downEntry.Combo) + " (hold)"
			ck := comboBaseString(downEntry.Combo)
			actionLabel := humanizeAction(downEntry.Entry.Action)
			groupName := sourceGroupName(downEntry.Entry.Source)
			isOverride := downEntry.Entry.Source.IsUser

			if search != "" &&
				!strings.Contains(strings.ToLower(comboDisplay), search) &&
				!strings.Contains(strings.ToLower(actionLabel), search) {
				continue
			}

			if isOverride {
				hasOverrides = true
			}

			ckJSON, _ := json.Marshal(ck)
			rowsBySource[groupName] = append(rowsBySource[groupName], keybindRowView{
				ComboDisplay: comboDisplay,
				ComboKey:     ck,
				ComboKeyJSON: template.JS(string(ckJSON)),
				ActionLabel:  actionLabel,
				BadgeClass:   ifStr(isOverride, "badge-user", "badge-core"),
				SourceLabel:  sourceBadgeLabel(downEntry.Entry.Source),
				IsOverride:   isOverride,
				IsHoldJSON:   "true",
			})
		} else {
			for _, e := range entries {
				comboDisplay := comboBaseString(e.Combo)
				ck := comboDisplay
				actionLabel := humanizeAction(e.Entry.Action)
				groupName := sourceGroupName(e.Entry.Source)
				isOverride := e.Entry.Source.IsUser

				if search != "" &&
					!strings.Contains(strings.ToLower(comboDisplay), search) &&
					!strings.Contains(strings.ToLower(actionLabel), search) {
					continue
				}

				if isOverride {
					hasOverrides = true
				}

				ckJSON2, _ := json.Marshal(ck)
				rowsBySource[groupName] = append(rowsBySource[groupName], keybindRowView{
					ComboDisplay: comboDisplay,
					ComboKey:     ck,
					ComboKeyJSON: template.JS(string(ckJSON2)),
					ActionLabel:  actionLabel,
					BadgeClass:   ifStr(isOverride, "badge-user", "badge-core"),
					SourceLabel:  sourceBadgeLabel(e.Entry.Source),
					IsOverride:   isOverride,
					IsHoldJSON:   "false",
				})
			}
		}
	}

	// Build sorted groups
	groups := make([]keybindGroupView, 0, len(rowsBySource))
	for sourceName, rows := range rowsBySource {
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].ComboDisplay < rows[j].ComboDisplay
		})
		groups = append(groups, keybindGroupView{SourceName: sourceName, Rows: rows})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].SourceName < groups[j].SourceName
	})

	data := KeybindSettingsData{
		Groups:         groups,
		HasOverrides:   hasOverrides,
		RemappingCombo: ps.RemappingCombo,
	}

	return renderTempl(KeybindSettings(data))
}

func ifStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func renderTempl(c templ.Component) string {
	return toolkit.RenderTempl("keyboard", c)
}
