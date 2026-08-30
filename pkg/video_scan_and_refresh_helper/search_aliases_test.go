package video_scan_and_refresh_helper

import (
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
)

func TestMergeSeriesSearchAliasesAddsRemoteTitlesWithoutDuplicates(t *testing.T) {
	existing := []string{"Local Show", " 本地剧名 "}
	mediaInfo := &models.MediaInfo{
		TitleCn:       "本地剧名",
		TitleEn:       "Remote Show",
		OriginalTitle: "  Original   Show  ",
	}

	got := mergeSeriesSearchAliases(existing, mediaInfo)
	want := []string{"Local Show", "本地剧名", "Remote Show", "Original Show"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeSeriesSearchAliases() = %#v, want %#v", got, want)
	}
}

func TestMergeSeriesSearchAliasesHandlesMissingRemoteMetadata(t *testing.T) {
	got := mergeSeriesSearchAliases([]string{" Existing   Alias ", "existing alias"}, nil)
	want := []string{"Existing Alias"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeSeriesSearchAliases(nil) = %#v, want %#v", got, want)
	}
}
