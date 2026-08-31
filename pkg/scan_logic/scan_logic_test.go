package scan_logic

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestExactVideoPathSkipWinsAcrossFormatterAndMediaCohort(t *testing.T) {
	tests := []struct {
		name      string
		videoType common.VideoType
		videoPath string
	}{
		{
			name:      "movie with normal formatter",
			videoType: common.Movie,
			videoPath: filepath.Join("media", "Movies", "Feature (2026)", "Feature.mkv"),
		},
		{
			name:      "series with emby formatter",
			videoType: common.Series,
			videoPath: filepath.Join("media", "Series", "Show", "Season 01", "Show.S01E02.mkv"),
		},
		{
			name:      "anime with emby formatter",
			videoType: common.Anime,
			videoPath: filepath.Join("media", "Anime", "Show", "Season 01", "Show.S01E03.mkv"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logic := &ScanLogic{}

			// Only the media-independent manual-upload record is true. A false
			// legacy record makes this test prove that Get checks the exact-path
			// domain first instead of accidentally succeeding through a formatter-
			// derived movie/series UID.
			logic.scanLogicMap.Store(models.GenerateUID4VideoPath(test.videoPath), true)
			if test.videoType == common.Movie {
				logic.scanLogicMap.Store(models.GenerateUID4Movie(test.videoPath), false)
			} else {
				legacy := models.NewSkipScanInfoBySeriesEx(test.videoPath, false)
				logic.scanLogicMap.Store(legacy.UID, false)
			}

			if !logic.Get(int(test.videoType), test.videoPath) {
				t.Fatalf("Get(%s, %q) missed the exact-path manual skip", test.videoType, test.videoPath)
			}
		})
	}
}

func TestExactSkipSetWinsOverConcurrentDatabaseMiss(t *testing.T) {
	videoPath := filepath.Join("media", "Movies", "Feature", "Feature.mkv")
	queryStarted := make(chan struct{})
	releaseQuery := make(chan struct{})
	logic := &ScanLogic{
		loadSkipInfosByUID: func(string) ([]models.SkipScanInfo, error) {
			close(queryStarted)
			<-releaseQuery
			return nil, nil
		},
		persistSkipScanInfo: func(*models.SkipScanInfo) error { return nil },
	}

	getDone := make(chan bool, 1)
	go func() {
		getDone <- logic.Get(int(common.Movie), videoPath)
	}()
	select {
	case <-queryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exact-path database miss")
	}
	if err := logic.SetExactVideoPathSkip(videoPath, true); err != nil {
		t.Fatal(err)
	}
	close(releaseQuery)
	select {
	case got := <-getDone:
		if !got {
			t.Fatal("stale database miss overwrote the concurrent exact skip")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent Get")
	}
	if !logic.Get(int(common.Movie), videoPath) {
		t.Fatal("exact skip was not retained after the concurrent miss")
	}
}

func TestExactSkipDatabaseReadFailureFailsClosedWithoutCaching(t *testing.T) {
	videoPath := filepath.Join("media", "Movies", "Feature", "Feature.mkv")
	logic := &ScanLogic{
		loadSkipInfosByUID: func(string) ([]models.SkipScanInfo, error) {
			return nil, errors.New("database unavailable")
		},
	}
	if !logic.getByUID(models.GenerateUID4VideoPath(videoPath), false) {
		t.Fatal("exact-path database failure did not fail closed")
	}
	if _, found := logic.scanLogicMap.Load(models.GenerateUID4VideoPath(videoPath)); found {
		t.Fatal("transient exact-path database failure was cached")
	}
}
