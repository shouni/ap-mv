package domain

import "testing"

func TestDecodeVideoRecipeJSONMapsLegacyFields(t *testing.T) {
	raw := []byte(`{
		"title": "legacy title",
		"theme": "legacy theme",
		"mood": "cinematic",
		"tempo": 132,
		"instruments": ["guitar", "drums"],
		"sections": [
			{"name": "Verse", "duration_seconds": 8, "prompt": "quiet verse"}
		],
		"lyrics": {
			"title": "lyric title",
			"theme": "lyric theme",
			"hook": "main hook",
			"lyrics": "line one"
		},
		"compose_mode": "sparkle_rock",
		"cuts": [
			{"duration_sec": 8, "audio_cue": "verse", "visual_anchor": "rooftop"}
		]
	}`)

	recipe, err := DecodeVideoRecipeJSON(raw)
	if err != nil {
		t.Fatalf("DecodeVideoRecipeJSON() error = %v", err)
	}
	if recipe.MusicRecipe.Title != "legacy title" {
		t.Fatalf("MusicRecipe.Title = %q", recipe.MusicRecipe.Title)
	}
	if recipe.MusicRecipe.Lyrics == nil || recipe.MusicRecipe.Lyrics.Lyrics != "line one" {
		t.Fatalf("MusicRecipe.Lyrics = %#v", recipe.MusicRecipe.Lyrics)
	}
	if recipe.Cuts[0].CutIndex != 1 {
		t.Fatalf("CutIndex = %d, want 1", recipe.Cuts[0].CutIndex)
	}
}

func TestValidateVideoRecipeRejectsOutOfRangeSectionIndex(t *testing.T) {
	recipe := &VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe: MusicRecipe{
			Sections: []MusicSection{{Name: "Verse", Duration: 8}},
		},
		Cuts: []VideoCut{
			{CutIndex: 1, SectionIndex: 2}, // out of range: recipe only has 1 section
		},
	}

	err := ValidateVideoRecipe(recipe)
	if err == nil {
		t.Fatal("ValidateVideoRecipe() error = nil, want out-of-range section_index error")
	}
}

func TestValidateVideoRecipeAllowsUnassignedSectionIndex(t *testing.T) {
	recipe := &VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe: MusicRecipe{
			Sections: []MusicSection{{Name: "Verse", Duration: 8}},
		},
		Cuts: []VideoCut{
			{CutIndex: 1, SectionIndex: 0}, // 0 = unassigned, must not be rejected
			{CutIndex: 2, SectionIndex: 1}, // in range
		},
	}

	if err := ValidateVideoRecipe(recipe); err != nil {
		t.Fatalf("ValidateVideoRecipe() error = %v, want nil", err)
	}
}
