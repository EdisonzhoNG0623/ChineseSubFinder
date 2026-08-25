package subhd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
	mu           sync.Mutex
	now          func() time.Time
	details      map[string]subHDDetailCacheEntry
	lists        map[string]subHDListCacheEntry
	negativePath string
	negative     map[string]time.Time
}

type persistedSubHDNegativeCache struct {
	Version int                  `json:"version"`
	Entries map[string]time.Time `json:"entries"`
}

func newSubHDSearchCache(persistencePath ...string) *subHDSearchCache {
	cache := &subHDSearchCache{
		now: time.Now, details: make(map[string]subHDDetailCacheEntry), lists: make(map[string]subHDListCacheEntry),
		negative: make(map[string]time.Time),
	}
	if len(persistencePath) > 0 {
		cache.negativePath = persistencePath[0]
		cache.loadNegative()
	}
	return cache
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
	if ok {
		if !entry.expiresAt.After(c.now()) {
			delete(c.details, key)
			return "", false
		}
		return entry.url, true
	}
	hash := subHDCacheKeyHash(key)
	expiresAt, ok := c.negative[hash]
	if !ok {
		return "", false
	}
	if !expiresAt.After(c.now()) {
		delete(c.negative, hash)
		return "", false
	}
	return "", true
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
	key = normalizeSubHDCacheKey(key)
	c.details[key] = subHDDetailCacheEntry{
		url: detailURL, storedAt: now, expiresAt: now.Add(ttl),
	}
	if detailURL == "" {
		c.negative[subHDCacheKeyHash(key)] = now.Add(ttl)
		c.evictNegative()
		c.persistNegative()
	} else if _, found := c.negative[subHDCacheKeyHash(key)]; found {
		delete(c.negative, subHDCacheKeyHash(key))
		c.persistNegative()
	}
	c.evictDetails()
}

func subHDCacheKeyHash(key string) string {
	sum := sha256.Sum256([]byte(normalizeSubHDCacheKey(key)))
	return hex.EncodeToString(sum[:])
}

func (c *subHDSearchCache) loadNegative() {
	if c.negativePath == "" {
		return
	}
	data, err := os.ReadFile(c.negativePath)
	if err != nil {
		return
	}
	state := persistedSubHDNegativeCache{}
	if json.Unmarshal(data, &state) != nil || state.Version != 1 {
		return
	}
	now := c.now()
	for key, expiresAt := range state.Entries {
		if len(c.negative) >= subHDSearchCacheLimit {
			break
		}
		if expiresAt.After(now) {
			c.negative[key] = expiresAt
		}
	}
}

func (c *subHDSearchCache) persistNegative() {
	if c.negativePath == "" {
		return
	}
	data, err := json.Marshal(persistedSubHDNegativeCache{Version: 1, Entries: c.negative})
	if err != nil || os.MkdirAll(filepath.Dir(c.negativePath), 0o755) != nil {
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(c.negativePath), ".subhd-negative-*")
	if err != nil {
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = os.Rename(tempPath, c.negativePath)
	}
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

func (c *subHDSearchCache) evictNegative() {
	for len(c.negative) > subHDSearchCacheLimit {
		oldestKey := ""
		var earliestExpiry time.Time
		for key, expiresAt := range c.negative {
			if oldestKey == "" || expiresAt.Before(earliestExpiry) {
				oldestKey, earliestExpiry = key, expiresAt
			}
		}
		delete(c.negative, oldestKey)
	}
}
