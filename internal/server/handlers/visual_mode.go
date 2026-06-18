package handlers

import (
	"net/http"
	"sort"
	"strings"
)

type VisualModeOption struct {
	ID        string
	Name      string
	IsDefault bool
}

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
			o.Modes[i].Name = displayVisualModeName(o.Modes[i].ID)
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

func displayVisualModeName(id string) string {
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
