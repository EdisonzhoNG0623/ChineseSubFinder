package series_helper

import (
	"os"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/media_info_dealers"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/unit_test_helper"
)

func TestReadSeriesInfoFromDir(t *testing.T) {
	t.Skip("requires a local Windows media-library fixture")

	//series := unit_test_helper.GetTestDataResourceRootPath([]string{"series", "Loki"}, 4, false)
	dealers := media_info_dealers.NewDealers(log_helper.GetLogger4Tester(), nil)
	seriesInfo, err := ReadSeriesInfoFromDir(dealers, "X:\\连续剧\\黑袍纠察队 (2019)", 90, false, true)
	if err != nil {
		t.Fatal(err)
	}

	println(seriesInfo.Name, seriesInfo.Year, seriesInfo.ImdbId)
	for i, info := range seriesInfo.EpList {
		println("Video:", i, info.Season, info.Episode)
		for j, subInfo := range info.SubAlreadyDownloadedList {
			println("Sub:", j, subInfo.Title, subInfo.Season, subInfo.Episode, subInfo.Language.String())
		}
	}
}

func TestGetSeriesListFromDirs(t *testing.T) {

	series := unit_test_helper.GetTestDataResourceRootPath([]string{"series"}, 4, false)
	if _, err := os.Stat(series); err != nil {
		t.Skip("external ChineseSubFinder-TestData fixture is not available")
	}
	got, err := GetSeriesListFromDirs(log_helper.GetLogger4Tester(), []string{series})
	if err != nil {
		t.Fatal(err)
	}

	if got.Size() < 1 {
		t.Fatal("GetSeriesListFromDirs got len < 1")
	}
}
