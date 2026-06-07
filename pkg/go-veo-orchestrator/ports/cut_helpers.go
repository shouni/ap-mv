package ports

import "sort"

// UniqueCharacterIDs はカットのスライスから重複しない CharacterID を抽出します。
func (cs Cuts) UniqueCharacterIDs() []string {
	set := make(map[string]struct{})
	for _, cut := range cs {
		if cut.CharacterID != "" {
			set[cut.CharacterID] = struct{}{}
		}
	}

	uniqueIDs := make([]string, 0, len(set))
	for id := range set {
		uniqueIDs = append(uniqueIDs, id)
	}
	sort.Strings(uniqueIDs)

	return uniqueIDs
}
