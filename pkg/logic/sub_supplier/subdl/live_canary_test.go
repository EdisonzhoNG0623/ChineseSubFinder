package subdl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/random_auth_key"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
)

func TestLiveSubDLCanary(t *testing.T) {
	if os.Getenv("SUBDL_LIVE_CANARY") != "1" {
		t.Skip("live canary disabled")
	}
	settings.SetConfigRootPath("/config")
	log := log_helper.GetLogger4Tester()
	pkg.ReadCustomAuthFile(log)
	authKey := random_auth_key.AuthKey{BaseKey: pkg.BaseKey(), AESKey16: pkg.AESKey16(), AESIv16: pkg.AESIv16()}
	fileDownloader := file_downloader.NewFileDownloader(cache_center.NewCacheCenter("SubDL-Canary-Secure", log), authKey)
	supplier := NewSupplier(fileDownloader)
	alive, _ := supplier.CheckAlive()
	if !alive {
		t.Fatal("SubDL live canary failed availability check")
	}

	fixtureRoot := t.TempDir()
	videoPath := filepath.Join(fixtureRoot, "Fight Club (1999).mkv")
	if err := os.WriteFile(videoPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	nfo := `<movie><title>Fight Club</title><originaltitle>Fight Club</originaltitle><year>1999</year><imdbid>tt0137523</imdbid><tmdbid>550</tmdbid><uniqueid type="imdb" default="true">tt0137523</uniqueid><uniqueid type="tmdb">550</uniqueid></movie>`
	if err := os.WriteFile(filepath.Join(fixtureRoot, "Fight Club (1999).nfo"), []byte(nfo), 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := supplier.GetSubListFromFile4Movie(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("SubDL returned no downloadable Chinese subtitle for the exact IMDb/TMDB canary")
	}
	for _, result := range results {
		if result.FromWhere != supplier.GetSupplierName() {
			t.Fatalf("unexpected supplier %q", result.FromWhere)
		}
		if len(result.Data) == 0 {
			t.Fatal("downloaded candidate has no data")
		}
		if result.FileUrl != credentialFreeURL(result.FileUrl) {
			t.Fatal("credential-bearing URL reached SubInfo")
		}
	}
	t.Logf("SubDL canary downloaded %d exact-ID Chinese candidate(s) into isolated cache", len(results))
}
