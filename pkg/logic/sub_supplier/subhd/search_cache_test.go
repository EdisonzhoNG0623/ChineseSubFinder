package subhd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubHDNegativeCachePersistsWithoutRawQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "negative-cache.json")
	cache := newSubHDSearchCache(path)
	cache.putDetail("https://example.invalid|Private Show", "")

	reloaded := newSubHDSearchCache(path)
	if value, ok := reloaded.getDetail("https://example.invalid|Private Show"); !ok || value != "" {
		t.Fatalf("persisted negative cache = %q, %v", value, ok)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Private Show") || strings.Contains(string(data), "example.invalid") {
		t.Fatalf("persistent negative cache exposed raw query: %s", data)
	}
}

func TestSubHDSearchCacheStoresPositiveAndNegativeResults(t *testing.T) {
	cache := newSubHDSearchCache()
	cache.putDetail("ROOT| Missing   Show ", "")
	if value, ok := cache.getDetail("root|missing show"); !ok || value != "" {
		t.Fatalf("negative detail cache = %q, %v", value, ok)
	}
	cache.putDetail("root|show", "/a/show")
	if value, ok := cache.getDetail("ROOT|SHOW"); !ok || value != "/a/show" {
		t.Fatalf("positive detail cache = %q, %v", value, ok)
	}
}

func TestSubHDSearchCacheExpiresAndCopiesLists(t *testing.T) {
	current := time.Date(2026, 8, 24, 1, 0, 0, 0, time.Local)
	cache := newSubHDSearchCache()
	cache.now = func() time.Time { return current }
	cache.putList("detail", []HdListItem{{Title: "original", Url: "/a/1"}})

	items, ok := cache.getList("detail")
	if !ok || len(items) != 1 {
		t.Fatalf("cached list = %#v, %v", items, ok)
	}
	items[0].Title = "mutated"
	items, ok = cache.getList("detail")
	if !ok || items[0].Title != "original" {
		t.Fatalf("cache returned shared mutable list: %#v", items)
	}

	current = current.Add(subHDPositiveCacheTTL + time.Second)
	if _, ok = cache.getList("detail"); ok {
		t.Fatal("expired list remained cached")
	}
}

func TestSubHDSearchCacheIsBoundedAndConcurrent(t *testing.T) {
	cache := newSubHDSearchCache()
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for index := 0; index < subHDSearchCacheLimit; index++ {
				key := fmt.Sprintf("%d-%d", worker, index)
				cache.putDetail(key, "/a/"+key)
				_, _ = cache.getDetail(key)
			}
		}(worker)
	}
	workers.Wait()
	if len(cache.details) > subHDSearchCacheLimit {
		t.Fatalf("detail cache size = %d, limit = %d", len(cache.details), subHDSearchCacheLimit)
	}
}

func TestSubHDListCacheIsBounded(t *testing.T) {
	cache := newSubHDSearchCache()
	for index := 0; index < subHDListCacheLimit*2; index++ {
		cache.putList(fmt.Sprintf("detail-%d", index), []HdListItem{{Title: fmt.Sprintf("item-%d", index)}})
	}
	if len(cache.lists) > subHDListCacheLimit {
		t.Fatalf("list cache size = %d, limit = %d", len(cache.lists), subHDListCacheLimit)
	}
}

func TestSubHDSearchPageBlocked(t *testing.T) {
	if !subHDSearchPageBlocked(`<script src="/cdn-cgi/challenge-platform/x.js"></script>`) {
		t.Fatal("challenge page was not detected")
	}
	if subHDSearchPageBlocked(`<div>共 0 条字幕</div>`) {
		t.Fatal("ordinary empty result was treated as verification")
	}
}

func TestSubHDSearchPageHasNoResultsRequiresExplicitMarker(t *testing.T) {
	if !subHDSearchPageHasNoResults(`<div>共 0 条字幕</div>`) {
		t.Fatal("explicit empty result was not detected")
	}
	if subHDSearchPageHasNoResults(`<html><body>temporarily incomplete</body></html>`) {
		t.Fatal("inconclusive page was treated as cacheable empty result")
	}
}
