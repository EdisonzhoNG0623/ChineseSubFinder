package subtitle_best_api

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/random_auth_key"
)

func TestSubtitleBestApiRejectsPlaceholderCredentialsWithoutNetwork(t *testing.T) {
	client := NewSubtitleBestApi(log_helper.GetLogger4Tester(), random_auth_key.AuthKey{
		BaseKey:  random_auth_key.BaseKey,
		AESKey16: random_auth_key.AESKey16,
		AESIv16:  random_auth_key.AESIv16,
	})
	if client == nil {
		t.Fatal("constructor returned nil")
	}
	if err := client.CheckAlive(); err == nil {
		t.Fatal("placeholder credentials unexpectedly passed validation")
	}
	if _, err := client.GetMediaInfo("tt0000001", "imdb", "movie"); err == nil {
		t.Fatal("placeholder credentials unexpectedly reached media lookup")
	}
	if _, err := client.ConvertId("1", "tmdb", "movie"); err == nil {
		t.Fatal("placeholder credentials unexpectedly reached id conversion")
	}
}
