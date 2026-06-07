package ports

import "testing"

func TestVideoRecipeNormalizeBuildsCutsFromVariableSections(t *testing.T) {
	recipe := &VideoRecipe{
		Title: "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜",
		Mood:  "Epic Symphonic Fantasy Rock Ballad, Emotional and Melancholic",
		Tempo: 72,
		Sections: []Section{
			{
				Name:            "Verse",
				DurationSeconds: 40,
				Prompt:          "[Silent Awakening] Focus strictly on the first lyrics block marked [Verse].",
			},
			{
				Name:            "Chorus",
				DurationSeconds: 45,
				Prompt:          "[Emotional Outburst & High-Voltage Peak] Focus on the lyrics marked [Chorus].",
			},
			{
				Name:            "Chorus 2",
				DurationSeconds: 55,
				Prompt:          "[The Final Peak & Ultimate Triumphant Finale] Focus on the final lyrics marked [Chorus 2].",
			},
		},
	}

	recipe.Normalize()

	if recipe.ProjectTitle != "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜" {
		t.Errorf("ProjectTitle = %q", recipe.ProjectTitle)
	}
	if recipe.MusicRecipe.TempoBPM != 72 {
		t.Errorf("TempoBPM = %d, want 72", recipe.MusicRecipe.TempoBPM)
	}
	if recipe.MusicRecipe.Style != recipe.Mood {
		t.Errorf("Style = %q, want %q", recipe.MusicRecipe.Style, recipe.Mood)
	}
	if recipe.MusicRecipe.TotalDurationSec != 140 {
		t.Errorf("TotalDurationSec = %.1f, want 140", recipe.MusicRecipe.TotalDurationSec)
	}
	if len(recipe.Cuts) != 3 {
		t.Fatalf("len(Cuts) = %d, want 3", len(recipe.Cuts))
	}
	if recipe.Cuts[2].StartSec != 85 || recipe.Cuts[2].EndSec != 140 {
		t.Errorf("third cut range = %.1f-%.1f, want 85-140", recipe.Cuts[2].StartSec, recipe.Cuts[2].EndSec)
	}
	if recipe.Cuts[0].VisualAnchor != "Verse" {
		t.Errorf("first cut VisualAnchor = %q, want Verse", recipe.Cuts[0].VisualAnchor)
	}
}
