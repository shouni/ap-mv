package domain

import "testing"

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
