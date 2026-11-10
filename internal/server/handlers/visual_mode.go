package handlers

import (
	"net/http"
	"sort"
	"strings"
)

// VisualModeOption is the information for one visual style selectable in the form.
type VisualModeOption struct {
	ID        string
	Name      string
	IsDefault bool
}

// VisualModeOptions holds the visual styles selectable in the form and the default selection.
type VisualModeOptions struct {
	Modes         []VisualModeOption
	DefaultModeID string
}

func (o *VisualModeOptions) normalize() {
	if len(o.Modes) == 0 {
		o.Modes = []VisualModeOption{{ID: "default", Name: "Default", IsDefault: true}}
		o.DefaultModeID = "default"
		return
	}
	for i := range o.Modes {
		o.Modes[i].ID = strings.TrimSpace(o.Modes[i].ID)
		o.Modes[i].Name = strings.TrimSpace(o.Modes[i].Name)
		if o.Modes[i].Name == "" {
			o.Modes[i].Name = DisplayVisualModeName(o.Modes[i].ID)
		}
	}
	sort.SliceStable(o.Modes, func(i, j int) bool {
		if o.Modes[i].ID == "default" {
			return true
		}
		if o.Modes[j].ID == "default" {
			return false
		}
		return o.Modes[i].Name < o.Modes[j].Name
	})
	if strings.TrimSpace(o.DefaultModeID) == "" {
		for _, mode := range o.Modes {
			if mode.IsDefault {
				o.DefaultModeID = mode.ID
				break
			}
		}
	}
	if strings.TrimSpace(o.DefaultModeID) == "" && len(o.Modes) > 0 {
		o.DefaultModeID = o.Modes[0].ID
	}
	for i := range o.Modes {
		o.Modes[i].IsDefault = o.Modes[i].ID == o.DefaultModeID
	}
}

func firstVisualModeOptions(options []VisualModeOptions) VisualModeOptions {
	if len(options) == 0 {
		return VisualModeOptions{}
	}
	return options[0]
}

func (o VisualModeOptions) applyToPageData(data PageData) PageData {
	o.normalize()
	data.VisualModes = o.Modes
	data.SelectedVisualMode = o.DefaultModeID
	return data
}

func (h *Handler) visualModeFromForm(r *http.Request) string {
	options := h.VisualOptions
	options.normalize()
	value := strings.TrimSpace(r.FormValue("visual_mode"))
	if value == "" {
		return options.DefaultModeID
	}
	for _, mode := range options.Modes {
		if value == mode.ID {
			return value
		}
	}
	return options.DefaultModeID
}

// DisplayVisualModeName builds a display name from a visual mode ID (underscore-separated to title
// case). When a hard-coded switch of mode names coexisted on the builder side, adding a new .md
// sometimes left a raw ID in the UI because the hand-maintained list was not updated. With one
// generic conversion, adding a mode needs no code change.
func DisplayVisualModeName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Default"
	}
	parts := strings.Fields(strings.ReplaceAll(id, "_", " "))
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		runes := []rune(parts[i])
		parts[i] = strings.ToUpper(string(runes[0])) + string(runes[1:])
	}
	return strings.Join(parts, " ")
}
