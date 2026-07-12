package domain

import (
	"strings"
	"testing"
)

func TestVideoHistoryDetailSectionGroupsBucketsCutsBySection(t *testing.T) {
	detail := VideoHistoryDetail{
		Sections: []VideoHistorySection{
			{SectionIndex: 0, Name: "Verse", StartSeconds: 0, EndSeconds: 20},
			{SectionIndex: 1, Name: "Chorus", StartSeconds: 20, EndSeconds: 40},
		},
		Cuts: []VideoHistoryCut{
			{CutIndex: 1, StartSec: 0},
			{CutIndex: 2, StartSec: 8},
			{CutIndex: 3, StartSec: 22},
			{CutIndex: 4, StartSec: 30},
		},
	}

	groups := detail.SectionGroups()
	if len(groups) != 2 {
		t.Fatalf("len(groups) = %d, want 2", len(groups))
	}
	if groups[0].Section.Name != "Verse" || len(groups[0].Cuts) != 2 {
		t.Errorf("group 0 = %+v, want Verse with 2 cuts", groups[0])
	}
	if groups[1].Section.Name != "Chorus" || len(groups[1].Cuts) != 2 {
		t.Errorf("group 1 = %+v, want Chorus with 2 cuts", groups[1])
	}
}

func TestVideoHistoryDetailSectionGroupsCollectsUnassignedCuts(t *testing.T) {
	detail := VideoHistoryDetail{
		Sections: []VideoHistorySection{
			{SectionIndex: 0, Name: "Verse", StartSeconds: 10, EndSeconds: 20},
		},
		Cuts: []VideoHistoryCut{
			// StartSec 0 is before the only section's start (10), so it can't be matched.
			{CutIndex: 1, StartSec: 0},
			{CutIndex: 2, StartSec: 12},
		},
	}

	groups := detail.SectionGroups()
	if len(groups) != 2 {
		t.Fatalf("len(groups) = %d, want 2 (Verse + unassigned)", len(groups))
	}
	if groups[0].Section.Name != "Verse" || len(groups[0].Cuts) != 1 {
		t.Errorf("group 0 = %+v, want Verse with 1 cut", groups[0])
	}
	if groups[1].Section.Name != "" || len(groups[1].Cuts) != 1 || groups[1].Cuts[0].CutIndex != 1 {
		t.Errorf("group 1 = %+v, want unassigned group containing cut 1", groups[1])
	}
}

func TestVideoHistoryDetailSectionGroupsReturnsNilWithoutSections(t *testing.T) {
	detail := VideoHistoryDetail{
		Cuts: []VideoHistoryCut{{CutIndex: 1}},
	}
	if groups := detail.SectionGroups(); groups != nil {
		t.Errorf("SectionGroups() = %+v, want nil", groups)
	}
}

func TestVideoHistoryCutAudioCuePartsSplitsDescriptionVocalAndSceneBeat(t *testing.T) {
	cut := VideoHistoryCut{
		AudioCue: "Verse: Rhythmic guitar chug builds momentum. Vocal: '時刻表なんて 気にしなくていい / 風が導く方へ 踏み出す' / scene beat 1/2: establish this section's emotion and motion",
	}
	parts := cut.AudioCueParts()
	if parts.Description != "Verse: Rhythmic guitar chug builds momentum." {
		t.Errorf("Description = %q", parts.Description)
	}
	if parts.Vocal != "時刻表なんて 気にしなくていい / 風が導く方へ 踏み出す" {
		t.Errorf("Vocal = %q", parts.Vocal)
	}
	if parts.SceneBeat != "scene beat 1/2: establish this section's emotion and motion" {
		t.Errorf("SceneBeat = %q", parts.SceneBeat)
	}
}

func TestVideoHistoryCutAudioCuePartsWithoutSceneBeat(t *testing.T) {
	cut := VideoHistoryCut{
		AudioCue: "Chorus: Anthemic chorus explosion, layered guitars and powerful drums. Vocal: 'さあ行こう 知らない街へ / 迷うことさえ 宝物にして'",
	}
	parts := cut.AudioCueParts()
	if parts.Description != "Chorus: Anthemic chorus explosion, layered guitars and powerful drums." {
		t.Errorf("Description = %q", parts.Description)
	}
	if parts.Vocal != "さあ行こう 知らない街へ / 迷うことさえ 宝物にして" {
		t.Errorf("Vocal = %q", parts.Vocal)
	}
	if parts.SceneBeat != "" {
		t.Errorf("SceneBeat = %q, want empty", parts.SceneBeat)
	}
}

func TestVideoHistoryCutAudioCuePartsWithoutVocalMarkerKeepsFullTextAsDescription(t *testing.T) {
	cut := VideoHistoryCut{AudioCue: "Instrumental build with no vocal line yet."}
	parts := cut.AudioCueParts()
	if parts.Description != "Instrumental build with no vocal line yet." {
		t.Errorf("Description = %q", parts.Description)
	}
	if parts.Vocal != "" {
		t.Errorf("Vocal = %q, want empty", parts.Vocal)
	}
}

func TestVideoHistoryCutAudioCuePartsHandlesVocalBeginsPhrasing(t *testing.T) {
	// Real recipe data uses varied phrasing ("Vocal begins:", not just "Vocal:") before the
	// quoted lyric line; the parser must match on "Vocal" + a quote, not an exact "Vocal:" label.
	cut := VideoHistoryCut{
		AudioCue: "Verse: Clean arpeggiated electric guitar intro. Vocal begins: '思い出だけを 鞄に詰めて / 知らない駅で 降りてみよう'",
	}
	parts := cut.AudioCueParts()
	if parts.Description != "Verse: Clean arpeggiated electric guitar intro." {
		t.Errorf("Description = %q", parts.Description)
	}
	if parts.Vocal != "思い出だけを 鞄に詰めて / 知らない駅で 降りてみよう" {
		t.Errorf("Vocal = %q", parts.Vocal)
	}
	if strings.Contains(parts.Description, "思い出だけを") {
		t.Errorf("lyric text leaked into Description: %q", parts.Description)
	}
}
