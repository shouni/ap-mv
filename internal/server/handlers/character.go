// Package handlers は、Web UI（フォーム表示・履歴閲覧・キーフレーム再生成等）の
// HTTPハンドラーを提供します。
package handlers

import (
	"net/http"
	"strings"
)

// CharacterOption は、フォームで選択可能なキャラクター1件分の情報です。
type CharacterOption struct {
	ID        string
	Name      string
	IsDefault bool
	// Seed はキャラクターに紐づく生成シードです（未設定の場合は nil）。
	Seed *int64
}

// CharacterOptions は、フォームで選択可能なキャラクター一覧とデフォルト選択を保持します。
type CharacterOptions struct {
	Characters         []CharacterOption
	DefaultCharacterID string
}

// normalize normalizes the provided values.
func (o *CharacterOptions) normalize() {
	seen := make(map[string]bool, len(o.Characters))
	normalized := make([]CharacterOption, 0, len(o.Characters))
	for _, char := range o.Characters {
		char.ID = strings.TrimSpace(char.ID)
		char.Name = strings.TrimSpace(char.Name)
		if char.ID == "" || seen[strings.ToLower(char.ID)] {
			continue
		}
		if char.Name == "" {
			char.Name = char.ID
		}
		if char.IsDefault && strings.TrimSpace(o.DefaultCharacterID) == "" {
			o.DefaultCharacterID = char.ID
		}
		seen[strings.ToLower(char.ID)] = true
		normalized = append(normalized, char)
	}
	o.Characters = normalized
	o.DefaultCharacterID = strings.TrimSpace(o.DefaultCharacterID)
	if o.DefaultCharacterID == "" && len(o.Characters) > 0 {
		o.DefaultCharacterID = o.Characters[0].ID
	}
}

// applyToPageData adds character selections to page data.
func (o CharacterOptions) applyToPageData(data PageData) PageData {
	o.normalize()
	data.Characters = o.Characters
	data.SelectedCharacterID = o.DefaultCharacterID
	return data
}

// selectedID returns a selected character ID, falling back to the default.
func (o CharacterOptions) selectedID(value string) string {
	value = strings.TrimSpace(value)
	o.normalize()
	if value == "" {
		return o.DefaultCharacterID
	}
	return value
}

// characterIDFromForm reads the character selection from a request form.
func (h *Handler) characterIDFromForm(r *http.Request) string {
	return h.CharacterOptions.selectedID(r.FormValue("character_id"))
}

// seed returns the configured seed for characterID, or nil if the character or its seed is unknown.
func (o CharacterOptions) seed(characterID string) *int64 {
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		return nil
	}
	for _, char := range o.Characters {
		if char.ID == characterID {
			return char.Seed
		}
	}
	return nil
}
