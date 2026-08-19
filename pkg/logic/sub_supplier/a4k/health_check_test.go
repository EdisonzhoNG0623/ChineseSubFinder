package a4k

import (
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestConfigureHealthCheckClientBoundsProbe(t *testing.T) {
	client := resty.New().SetRetryCount(3)
	configured := configureHealthCheckClient(client)

	if configured != client {
		t.Fatal("health-check configuration should reuse the supplier client")
	}
	if got := client.GetClient().Timeout; got != a4kHealthCheckTimeout {
		t.Fatalf("timeout = %v, want %v", got, a4kHealthCheckTimeout)
	}
	if got := client.RetryCount; got != 0 {
		t.Fatalf("retry count = %d, want 0", got)
	}
}

func TestIsA4kSubtitlePageRejectsParkedDomain(t *testing.T) {
	if isA4kSubtitlePage(`<title>NameBright - Coming Soon</title><h1>a4k.net is coming soon</h1>`) {
		t.Fatal("parked domain page must not pass supplier health check")
	}
	if !isA4kSubtitlePage(`<title>首页 | A4K字幕</title><ul class="sub-item-list"></ul>`) {
		t.Fatal("expected A4K subtitle page to pass supplier health check")
	}
}

func TestA4kResultMatchesEpisode(t *testing.T) {
	if !a4kResultMatchesEpisode(SearchResultItem{Season: 8, Episode: 11}, 8, 11) {
		t.Fatal("exact episode should match")
	}
	if !a4kResultMatchesEpisode(SearchResultItem{Season: 8, IsFullSeason: true}, 8, 11) {
		t.Fatal("same-season bundle should match")
	}
	if a4kResultMatchesEpisode(SearchResultItem{Season: 8, Episode: 12}, 8, 11) {
		t.Fatal("wrong episode must be rejected")
	}
	if a4kResultMatchesEpisode(SearchResultItem{Season: 7, IsFullSeason: true}, 8, 11) {
		t.Fatal("wrong-season bundle must be rejected")
	}
}
