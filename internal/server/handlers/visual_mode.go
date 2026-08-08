package handlers

import (
	"net/http"
	"sort"
	"strings"
)

// VisualModeOption は、フォームで選択可能な映像スタイル1件分の情報です。
type VisualModeOption struct {
	ID        string
	Name      string
	IsDefault bool
}

// VisualModeOptions は、フォームで選択可能な映像スタイル一覧とデフォルト選択を保持します。
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

// DisplayVisualModeName は visual mode の ID から表示名を作ります（アンダースコア区切りを
// タイトルケースへ）。builder 側にモード名のハードコード switch が併存していた頃は、
// 新しい .md を追加すると UI に raw ID が出る手作業リストの更新漏れがありました。
// 生成的な変換 1 本にしたので、モード追加時のコード変更は不要です。
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
