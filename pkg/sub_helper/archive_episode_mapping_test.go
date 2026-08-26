package sub_helper

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

func TestOrganizeDlSubFilesForSeriesMapsExtractedCollection(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"Season 04/简_35.ass", "Season 04/[36] 繁体.srt", "Season 04/1080p.ass"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(bytes.Repeat([]byte("subtitle\n"), 256)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	const tempName = "archive-episode-mapping-test"
	tempPath, err := pkg.GetTmpFolderByName(tempName)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempPath)
	organized, err := OrganizeDlSubFilesForSeries(logrus.New(), tempName, []supplier.SubInfo{{
		FromWhere: "test", Name: "season.zip", Ext: ".zip", Data: archive.Bytes(),
		Season: 4, Episode: 0, IsFullSeason: true,
	}}, &series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35},
		{Season: 4, Episode: 36},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(organized["S4E35"]) != 1 || len(organized["S4E36"]) != 1 {
		t.Fatalf("organized collection = %#v", organized)
	}
	if _, exists := organized["S4E1080"]; exists {
		t.Fatal("video resolution was treated as an episode")
	}
}
