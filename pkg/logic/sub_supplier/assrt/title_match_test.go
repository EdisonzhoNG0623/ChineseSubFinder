package assrt

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
)

func TestAssrtCandidateMatchesMedia(t *testing.T) {
	tests := []struct {
		name      string
		mediaInfo models.MediaInfo
		candidate string
		want      bool
	}{
		{
			name:      "english alias and episode match",
			mediaInfo: models.MediaInfo{TitleCn: "真实的人类", TitleEn: "Humans", OriginalTitle: "Humans", Year: "2015-06-14"},
			candidate: "Humans.S03E04.Episode.4.720p.AMZN.WEB-DL.ass",
			want:      true,
		},
		{
			name:      "translated english alias match",
			mediaInfo: models.MediaInfo{TitleCn: "善意的竞争", TitleEn: "Friendly Rivalry", OriginalTitle: "Seonuiui Gyeongjaeng", Year: "2025-02-10"},
			candidate: "Friendly Rivalry S01E07 1080p NF WEB-DL.CHS.srt",
			want:      true,
		},
		{
			name:      "wrong title with same episode",
			mediaInfo: models.MediaInfo{TitleCn: "我的恶魔", TitleEn: "My Demon", OriginalTitle: "My Demon", Year: "2023-11-24"},
			candidate: "Reaper.S01E05.HDTV.XviD-XOR-chs.srt",
			want:      false,
		},
		{
			name:      "different english title",
			mediaInfo: models.MediaInfo{TitleCn: "女巫", TitleEn: "A Discovery of Witches", OriginalTitle: "A Discovery of Witches", Year: "2020-01-01"},
			candidate: "Agatha.All.Along.S01E09.1080p.DSNP.WEB-DL.srt",
			want:      false,
		},
		{
			name:      "sequel is not original title",
			mediaInfo: models.MediaInfo{TitleCn: "地球脉动", TitleEn: "Planet Earth", OriginalTitle: "Planet Earth", Year: "2006-03-05"},
			candidate: "Planet.Earth.II.S01E04.2160p.UHD.BluRay.ass",
			want:      false,
		},
		{
			name:      "later sequel is not original title",
			mediaInfo: models.MediaInfo{TitleCn: "地球脉动", TitleEn: "Planet Earth", OriginalTitle: "Planet Earth", Year: "2006-03-05"},
			candidate: "Planet.Earth.III.S01E07.Human.2160p.WEB-DL.ass",
			want:      false,
		},
		{
			name:      "matching sequel remains valid",
			mediaInfo: models.MediaInfo{TitleCn: "地球脉动 第二季", TitleEn: "Planet Earth II", OriginalTitle: "Planet Earth II", Year: "2016-11-06"},
			candidate: "Planet.Earth.II.S01E04.2160p.UHD.BluRay.ass",
			want:      true,
		},
		{
			name:      "wrong release year rejected",
			mediaInfo: models.MediaInfo{TitleCn: "同名剧", TitleEn: "Same Name", OriginalTitle: "Same Name", Year: "2023-01-01"},
			candidate: "Same.Name.2020.S01E01.WEB-DL.srt",
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := assrtCandidateMatchesMedia(&test.mediaInfo, test.candidate); got != test.want {
				t.Fatalf("assrtCandidateMatchesMedia(%q) = %v, want %v", test.candidate, got, test.want)
			}
		})
	}
}

func TestAssrtCandidateFieldsMatchMediaRejectsConflictingIdentity(t *testing.T) {
	mediaInfo := models.MediaInfo{
		TitleCn: "地球脉动", TitleEn: "Planet Earth", OriginalTitle: "Planet Earth", Year: "2006-03-05",
	}

	if matched, rejected := assrtCandidateFieldsMatchMedia(&mediaInfo, 4,
		"地球脉动 第1季 S01E04", "Planet.Earth.II.S01E04.2160p.UHD.BluRay.ass"); matched || rejected == "" {
		t.Fatalf("conflicting ASSRT fields accepted: matched=%v rejected=%q", matched, rejected)
	}
	if matched, rejected := assrtCandidateFieldsMatchMedia(&mediaInfo, 4,
		"地球脉动 第1季 S01E04", "Planet.Earth.S01E04.1080p.BluRay.ass"); !matched || rejected != "" {
		t.Fatalf("consistent ASSRT fields rejected: matched=%v rejected=%q", matched, rejected)
	}
}

func TestAssrtCandidateMatchesEpisodeCollection(t *testing.T) {
	mediaInfo := models.MediaInfo{TitleCn: "死神", TitleEn: "Bleach", OriginalTitle: "Bleach", Year: "2004-10-05"}
	candidate := "死神 bleach 001-366"

	if !assrtCandidateMatchesMediaForEpisode(&mediaInfo, candidate, 31) {
		t.Fatal("matching bilingual episode collection was rejected")
	}
	if assrtCandidateMatchesMediaForEpisode(&mediaInfo, candidate, 367) {
		t.Fatal("episode outside collection range was accepted")
	}
}

func TestAssrtSearchKeywordTypesUsesUniqueAliases(t *testing.T) {
	mediaInfo := &models.MediaInfo{
		TitleCn:       "妖精的尾巴",
		TitleEn:       "Fairy Tail",
		OriginalTitle: "Fairy Tail",
	}
	got := assrtSearchKeywordTypes(mediaInfo)
	if len(got) != 2 || got[0] != "cn" || got[1] != "en" {
		t.Fatalf("keyword types = %#v, want [cn en]", got)
	}

	got = assrtSearchKeywordTypes(&models.MediaInfo{TitleEn: "Fairy Tail"})
	if len(got) != 1 || got[0] != "en" {
		t.Fatalf("empty Chinese title should fall back to English, got %#v", got)
	}
}

func TestAssrtCandidateMatchesAbsoluteEpisode(t *testing.T) {
	mediaInfo := &models.MediaInfo{TitleCn: "妖精的尾巴", TitleEn: "Fairy Tail", OriginalTitle: "Fairy Tail"}
	if !assrtCandidateMatchesMediaForEpisodes(mediaInfo, "Fairy Tail 288 CHS", []int{11, 288}) {
		t.Fatal("absolute episode candidate was rejected")
	}
	if assrtCandidateMatchesMediaForEpisodes(mediaInfo, "Fairy Tail 289 CHS", []int{11, 288}) {
		t.Fatal("wrong absolute episode candidate was accepted")
	}
}

func TestAssrtCandidateMatchesBilingualArchiveExtension(t *testing.T) {
	mediaInfo := &models.MediaInfo{TitleCn: "家庭教师", TitleEn: "REBORN!", OriginalTitle: "家庭教師ヒットマンREBORN!", Year: "2006"}
	for _, candidate := range []string{
		"家庭教師ヒットマンREBORN!",
		"家庭教師ヒットマンREBORN！.zip",
	} {
		if !assrtCandidateMatchesMediaForEpisodes(mediaInfo, candidate, []int{48}) {
			t.Fatalf("matching bilingual archive title %q was rejected", candidate)
		}
	}
}

func TestAssrtProviderSearchPlanPrioritizesAiredAndBareAbsolute(t *testing.T) {
	mediaInfo := &models.MediaInfo{TitleCn: "妖精的尾巴", TitleEn: "Fairy Tail", OriginalTitle: "Fairy Tail"}
	plan := assrtProviderSearchPlan(mediaInfo, 8, 11, 288)
	if len(plan) != 6 {
		t.Fatalf("plan length = %d, want 6: %#v", len(plan), plan)
	}
	want := map[string]bool{
		"妖精的尾巴 S08E11":      false,
		"Fairy Tail S08E11": false,
		"妖精的尾巴 288":         false,
		"Fairy Tail 288":    false,
	}
	for _, query := range plan {
		if _, exists := want[query.Query]; exists {
			want[query.Query] = true
		}
	}
	for query, found := range want {
		if !found {
			t.Fatalf("missing prioritized query %q in %#v", query, plan)
		}
	}
}
