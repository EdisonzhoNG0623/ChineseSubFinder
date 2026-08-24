package subhd

import (
	"strings"
	"sync"
	"time"
)

const (
	subHDPositiveCacheTTL = 24 * time.Hour
	subHDNegativeCacheTTL = 24 * time.Hour
	subHDSearchCacheLimit = 512
	subHDListCacheLimit   = 128
)

type subHDDetailCacheEntry struct {
	url       string
	expiresAt time.Time
	storedAt  time.Time
}

type subHDListCacheEntry struct {
	items     []HdListItem
	expiresAt time.Time
	storedAt  time.Time
}

type subHDSearchCache struct {
	mu      sync.Mutex
	now     func() time.Time
	details map[string]subHDDetailCacheEntry
	lists   map[string]subHDListCacheEntry
}

func newSubHDSearchCache() *subHDSearchCache {
	return &subHDSearchCache{
		now: time.Now, details: make(map[string]subHDDetailCacheEntry), lists: make(map[string]subHDListCacheEntry),
	}
}

func normalizeSubHDCacheKey(value string) string {
	parts := strings.Split(value, "|")
	for index := range parts {
		parts[index] = strings.Join(strings.Fields(parts[index]), " ")
	}
	return strings.ToLower(strings.Join(parts, "|"))
}

func (c *subHDSearchCache) getDetail(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key = normalizeSubHDCacheKey(key)
	entry, ok := c.details[key]
	if !ok {
		return "", false
	}
	if !entry.expiresAt.After(c.now()) {
		delete(c.details, key)
		return "", false
	}
	return entry.url, true
}

func (c *subHDSearchCache) putDetail(key, detailURL string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	ttl := subHDPositiveCacheTTL
	if detailURL == "" {
		ttl = subHDNegativeCacheTTL
	}
	c.details[normalizeSubHDCacheKey(key)] = subHDDetailCacheEntry{
		url: detailURL, storedAt: now, expiresAt: now.Add(ttl),
	}
	c.evictDetails()
}

func (c *subHDSearchCache) getList(key string) ([]HdListItem, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key = normalizeSubHDCacheKey(key)
	entry, ok := c.lists[key]
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.After(c.now()) {
		delete(c.lists, key)
		return nil, false
	}
	return append([]HdListItem(nil), entry.items...), true
}

func (c *subHDSearchCache) putList(key string, items []HdListItem) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	ttl := subHDPositiveCacheTTL
	if len(items) == 0 {
		ttl = subHDNegativeCacheTTL
	}
	c.lists[normalizeSubHDCacheKey(key)] = subHDListCacheEntry{
		items: append([]HdListItem(nil), items...), storedAt: now, expiresAt: now.Add(ttl),
	}
	c.evictLists()
}

func (c *subHDSearchCache) evictDetails() {
	for len(c.details) > subHDSearchCacheLimit {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range c.details {
			if oldestKey == "" || entry.storedAt.Before(oldest) {
				oldestKey, oldest = key, entry.storedAt
			}
		}
		delete(c.details, oldestKey)
	}
}

func (c *subHDSearchCache) evictLists() {
	for len(c.lists) > subHDListCacheLimit {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range c.lists {
			if oldestKey == "" || entry.storedAt.Before(oldest) {
				oldestKey, oldest = key, entry.storedAt
			}
		}
		delete(c.lists, oldestKey)
	}
}
