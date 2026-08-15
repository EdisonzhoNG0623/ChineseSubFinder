package settings

import (
	"testing"
	"time"
)

func TestDownloadFileCacheLegacyTTLIsMigrated(t *testing.T) {
	cache := &DownloadFileCache{TTL: 4320, Unit: "hour"}
	cache.Check()
	if cache.TTL != 336 || cache.Unit != "hour" {
		t.Fatalf("legacy cache settings were not migrated: %+v", cache)
	}
	if cache.Duration() != 14*24*time.Hour {
		t.Fatalf("unexpected cache duration: %s", cache.Duration())
	}
}

func TestDownloadFileCacheKeepsValidCustomTTL(t *testing.T) {
	cache := &DownloadFileCache{TTL: 168, Unit: "hour"}
	cache.Check()
	if cache.TTL != 168 || cache.Duration() != 7*24*time.Hour {
		t.Fatalf("valid custom cache settings changed: %+v", cache)
	}
}
