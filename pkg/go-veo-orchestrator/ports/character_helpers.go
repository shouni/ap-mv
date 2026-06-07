package ports

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GetCharacter は、指定されたID（またはその小文字版）からキャラクター情報を特定します。
// マップに存在する場合はそのポインタを返し、存在しない場合は nil を返します。
func (c *Characters) GetCharacter(ID string) *Character {
	if c == nil || c.ByID == nil {
		return nil
	}

	// 1. 直接のIDで検索、見つからなければ小文字に正規化して再検索
	char, ok := c.ByID[ID]
	if !ok {
		char, ok = c.ByID[strings.ToLower(ID)]
	}

	if ok {
		return char
	}

	return nil
}

// GetDefault は定義順に IsDefault が true のキャラクターを1人返します。
func (c *Characters) GetDefault() *Character {
	if c == nil {
		return nil
	}

	for i := range c.List {
		if c.List[i].IsDefault {
			return &c.List[i]
		}
	}

	return nil
}

// GetCharacterWithDefault は、指定されたIDでキャラクターを検索し、見つからない場合はデフォルトのキャラクターを返します。
// どちらのキャラクターも見つからない場合は nil を返します。
func (c *Characters) GetCharacterWithDefault(ID string) *Character {
	char := c.GetCharacter(ID)
	if char != nil {
		return char
	}
	char = c.GetDefault()
	if char != nil {
		return char
	}

	return nil
}

// NewCharacters はキャラクター定義を検証し、ID検索用インデックスを構築します。
func NewCharacters(list []Character) (*Characters, error) {
	chars := &Characters{
		List: list,
		ByID: make(map[string]*Character, len(list)*2),
	}
	if err := chars.Validate(); err != nil {
		return nil, err
	}

	for i := range chars.List {
		char := &chars.List[i]
		chars.ByID[char.ID] = char
		chars.ByID[strings.ToLower(char.ID)] = char
	}
	return chars, nil
}

// ParseCharacters はJSONバイト列からキャラクター定義をパースして返します。
func ParseCharacters(charactersJSON []byte) (*Characters, error) {
	var list []Character
	if err := json.Unmarshal(charactersJSON, &list); err != nil {
		return nil, fmt.Errorf("キャラクター情報のJSONパースに失敗しました: %w", err)
	}

	chars, err := NewCharacters(list)
	if err != nil {
		return nil, err
	}
	return chars, nil
}

// Validate はキャラクター定義の設定ミスを検出します。
func (c *Characters) Validate() error {
	if c == nil {
		return fmt.Errorf("キャラクター定義が空です")
	}
	seen := make(map[string]struct{}, len(c.List))
	defaultCount := 0
	for i, char := range c.List {
		id := strings.TrimSpace(char.ID)
		if id == "" {
			return fmt.Errorf("キャラクターIDが空です (index: %d)", i)
		}
		if char.ID != id {
			return fmt.Errorf("キャラクターIDに前後の空白があります: %q", char.ID)
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("キャラクターIDが重複しています: %s", id)
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(char.Name) == "" {
			return fmt.Errorf("キャラクター名が空です (id: %s)", id)
		}
		if strings.TrimSpace(char.ReferenceURL) == "" {
			return fmt.Errorf("参照画像URLが空です (id: %s)", id)
		}
		if len(char.VisualCues) == 0 {
			return fmt.Errorf("visual_cuesが空です (id: %s)", id)
		}
		if char.IsDefault {
			defaultCount++
		}
	}
	if defaultCount > 1 {
		return fmt.Errorf("デフォルトキャラクターが複数あります")
	}
	return nil
}
