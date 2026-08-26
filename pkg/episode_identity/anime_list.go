package episode_identity

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	DefaultAnimeListURL = "https://raw.githubusercontent.com/Anime-Lists/anime-lists/master/anime-list.xml"
	maxAnimeListSize    = 8 << 20
)

var ErrNoMapping = errors.New("episode numbering mapping not found")

type animeListDocument struct {
	Anime []animeListEntry `xml:"anime"`
}

type animeListEntry struct {
	AniDBID           string             `xml:"anidbid,attr"`
	TVDBID            string             `xml:"tvdbid,attr"`
	IMDbID            string             `xml:"imdbid,attr"`
	TMDBTV            string             `xml:"tmdbtv,attr"`
	DefaultTVDBSeason string             `xml:"defaulttvdbseason,attr"`
	EpisodeOffset     int                `xml:"episodeoffset,attr"`
	Name              string             `xml:"name"`
	Mappings          []animeListMapping `xml:"mapping-list>mapping"`
}

type animeListMapping struct {
	TVDBSeason int `xml:"tvdbseason,attr"`
	Start      int `xml:"start,attr"`
	End        int `xml:"end,attr"`
	Offset     int `xml:"offset,attr"`
}

type AnimeListResolver struct {
	document animeListDocument
	byTMDB   map[string][]int
	byTVDB   map[string][]int
	byIMDb   map[string][]int
	byName   map[string][]int
}

func ParseAnimeList(reader io.Reader) (*AnimeListResolver, error) {
	var document animeListDocument
	decoder := xml.NewDecoder(io.LimitReader(reader, maxAnimeListSize+1))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Anime-Lists document: %w", err)
	}
	if len(document.Anime) == 0 {
		return nil, errors.New("Anime-Lists document contains no entries")
	}
	resolver := &AnimeListResolver{
		document: document,
		byTMDB:   make(map[string][]int),
		byTVDB:   make(map[string][]int),
		byIMDb:   make(map[string][]int),
		byName:   make(map[string][]int),
	}
	for index, entry := range document.Anime {
		resolver.indexEntry(resolver.byTMDB, entry.TMDBTV, index)
		resolver.indexEntry(resolver.byTVDB, entry.TVDBID, index)
		resolver.indexEntry(resolver.byIMDb, strings.ToLower(strings.TrimSpace(entry.IMDbID)), index)
		resolver.indexEntry(resolver.byName, normalizeAnimeTitle(entry.Name), index)
	}
	return resolver, nil
}

func (r *AnimeListResolver) Resolve(ctx context.Context, request Request) (Identity, error) {
	candidates, err := r.ResolveCandidates(ctx, request)
	if err != nil {
		return Identity{}, err
	}
	if len(candidates) == 0 {
		return Identity{}, ErrNoMapping
	}
	if len(candidates) > 1 {
		return Identity{}, fmt.Errorf("conflicting Anime-Lists mappings: %d candidates", len(candidates))
	}
	return candidates[0], nil
}

func (r *AnimeListResolver) ResolveCandidates(_ context.Context, request Request) ([]Identity, error) {
	if request.Season <= 0 || request.Episode <= 0 {
		return nil, errors.New("season and episode must be positive")
	}
	if strings.TrimSpace(request.IDs.TMDB) == "" && strings.TrimSpace(request.IDs.TVDB) == "" &&
		strings.TrimSpace(request.IDs.IMDb) == "" && len(request.Aliases) == 0 && strings.TrimSpace(request.SeriesName) == "" {
		return nil, errors.New("at least one stable series ID or title alias is required")
	}

	resolved := make([]Identity, 0, 2)
	seenAbsolute := make(map[int]struct{}, 2)
	matchedByTitle := !hasStableSeriesID(request.IDs)
	for _, entryIndex := range r.candidateIndexes(request) {
		entry := r.document.Anime[entryIndex]
		absolute, ok := entry.resolveAbsolute(request.Season, request.Episode)
		if !ok {
			continue
		}
		candidate := Identity{
			IDs: ExternalIDs{
				IMDb: firstNonEmpty(request.IDs.IMDb, entry.IMDbID), TMDB: firstNonEmpty(request.IDs.TMDB, entry.TMDBTV),
				TVDB: firstNonEmpty(request.IDs.TVDB, entry.TVDBID), AniDB: entry.AniDBID,
			},
			Season: request.Season, Episode: request.Episode, AbsoluteEpisode: absolute,
			Confidence: 1,
			Evidence: []Evidence{{
				Source: "Anime-Lists", Rule: fmt.Sprintf("AniDB %s (%s) offset mapping", entry.AniDBID, entry.Name), Confidence: 1,
			}},
		}
		if matchedByTitle {
			candidate.Confidence = 0.92
			candidate.Evidence[0] = Evidence{Source: "Anime-Lists title", Rule: "normalized title and season mapping", Confidence: 0.92}
		}
		if _, duplicate := seenAbsolute[candidate.AbsoluteEpisode]; duplicate {
			continue
		}
		seenAbsolute[candidate.AbsoluteEpisode] = struct{}{}
		resolved = append(resolved, candidate)
	}
	return resolved, nil
}

func (r *AnimeListResolver) MatchesSeries(_ context.Context, request Request) (bool, error) {
	if strings.TrimSpace(request.IDs.TMDB) == "" && strings.TrimSpace(request.IDs.TVDB) == "" &&
		strings.TrimSpace(request.IDs.IMDb) == "" && len(request.Aliases) == 0 && strings.TrimSpace(request.SeriesName) == "" {
		return false, errors.New("at least one stable series ID or title alias is required")
	}
	return len(r.candidateIndexes(request)) > 0, nil
}

func (r *AnimeListResolver) indexEntry(index map[string][]int, id string, entryIndex int) {
	id = normalizeExternalID(id)
	if id == "" {
		return
	}
	index[id] = append(index[id], entryIndex)
}

func (r *AnimeListResolver) candidateIndexes(request Request) []int {
	out := make([]int, 0, 4)
	seen := make(map[int]struct{}, 4)
	appendIndexes := func(indexes []int) {
		for _, entryIndex := range indexes {
			if _, exists := seen[entryIndex]; exists {
				continue
			}
			seen[entryIndex] = struct{}{}
			out = append(out, entryIndex)
		}
	}
	appendIndexes(r.byTMDB[normalizeExternalID(request.IDs.TMDB)])
	appendIndexes(r.byTVDB[normalizeExternalID(request.IDs.TVDB)])
	appendIndexes(r.byIMDb[strings.ToLower(strings.TrimSpace(request.IDs.IMDb))])
	if len(out) == 0 && !hasStableSeriesID(request.IDs) {
		for _, alias := range append([]string{request.SeriesName}, request.Aliases...) {
			appendIndexes(r.byName[normalizeAnimeTitle(alias)])
		}
	}
	return out
}

func hasStableSeriesID(ids ExternalIDs) bool {
	return strings.TrimSpace(ids.TMDB) != "" || strings.TrimSpace(ids.TVDB) != "" || strings.TrimSpace(ids.IMDb) != ""
}

func normalizeAnimeTitle(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if len(value) >= 6 && value[len(value)-1] == ')' {
		open := strings.LastIndex(value, "(")
		if open >= 0 {
			year := value[open+1 : len(value)-1]
			if len(year) == 4 {
				if _, err := strconv.Atoi(year); err == nil {
					value = strings.TrimSpace(value[:open])
				}
			}
		}
	}
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

func normalizeExternalID(value string) string {
	value = strings.TrimSpace(value)
	if _, err := strconv.Atoi(value); err == nil {
		return strings.TrimLeft(value, "0")
	}
	return strings.ToLower(value)
}

func animeEntryMatchesIDs(entry animeListEntry, ids ExternalIDs) bool {
	if ids.TMDB != "" && entry.TMDBTV != "" && sameNumericID(ids.TMDB, entry.TMDBTV) {
		return true
	}
	if ids.TVDB != "" && entry.TVDBID != "" && sameNumericID(ids.TVDB, entry.TVDBID) {
		return true
	}
	return ids.IMDb != "" && entry.IMDbID != "" && strings.EqualFold(strings.TrimSpace(ids.IMDb), strings.TrimSpace(entry.IMDbID))
}

func sameNumericID(left, right string) bool {
	left = strings.TrimLeft(strings.TrimSpace(left), "0")
	right = strings.TrimLeft(strings.TrimSpace(right), "0")
	return left != "" && left == right
}

func (entry animeListEntry) resolveAbsolute(season, episode int) (int, bool) {
	for _, mapping := range entry.Mappings {
		if mapping.TVDBSeason != season || mapping.Start <= 0 || mapping.End < mapping.Start {
			continue
		}
		animeEpisode := episode - mapping.Offset
		if animeEpisode < mapping.Start || animeEpisode > mapping.End {
			continue
		}
		return entry.EpisodeOffset + animeEpisode, true
	}

	defaultSeason, err := strconv.Atoi(entry.DefaultTVDBSeason)
	if err == nil && defaultSeason == season && len(entry.Mappings) == 0 {
		return entry.EpisodeOffset + episode, true
	}
	return 0, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// CachedAnimeListResolver lazily refreshes Anime-Lists and falls back to a
// stale on-disk copy when the source is temporarily unavailable.
type CachedAnimeListResolver struct {
	client    *http.Client
	sourceURL string
	cachePath string
	ttl       time.Duration

	mu       sync.Mutex
	resolver *AnimeListResolver
	loadedAt time.Time
}

func NewCachedAnimeListResolver(client *http.Client, cachePath string, ttl time.Duration) *CachedAnimeListResolver {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &CachedAnimeListResolver{client: client, sourceURL: DefaultAnimeListURL, cachePath: cachePath, ttl: ttl}
}

func (r *CachedAnimeListResolver) Resolve(ctx context.Context, request Request) (Identity, error) {
	resolver, err := r.load(ctx)
	if err != nil {
		return Identity{}, err
	}
	return resolver.Resolve(ctx, request)
}

func (r *CachedAnimeListResolver) ResolveCandidates(ctx context.Context, request Request) ([]Identity, error) {
	resolver, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	return resolver.ResolveCandidates(ctx, request)
}

func (r *CachedAnimeListResolver) MatchesSeries(ctx context.Context, request Request) (bool, error) {
	resolver, err := r.load(ctx)
	if err != nil {
		return false, err
	}
	return resolver.MatchesSeries(ctx, request)
}

func (r *CachedAnimeListResolver) load(ctx context.Context) (*AnimeListResolver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolver != nil && (r.ttl <= 0 || time.Since(r.loadedAt) < r.ttl) {
		return r.resolver, nil
	}
	if cached, info, err := readCache(r.cachePath); err == nil &&
		(r.ttl <= 0 || time.Since(info.ModTime()) < r.ttl) {
		resolver, parseErr := ParseAnimeList(bytes.NewReader(cached))
		if parseErr == nil {
			r.resolver = resolver
			r.loadedAt = info.ModTime()
			return resolver, nil
		}
	}

	data, fetchErr := r.fetch(ctx)
	if fetchErr == nil {
		resolver, parseErr := ParseAnimeList(bytes.NewReader(data))
		if parseErr == nil {
			r.resolver = resolver
			r.loadedAt = time.Now()
			_ = writeCacheAtomically(r.cachePath, data)
			return resolver, nil
		}
		fetchErr = parseErr
	}

	cached, cacheErr := os.ReadFile(r.cachePath)
	if cacheErr == nil {
		resolver, parseErr := ParseAnimeList(bytes.NewReader(cached))
		if parseErr == nil {
			r.resolver = resolver
			r.loadedAt = time.Now()
			return resolver, nil
		}
		cacheErr = parseErr
	}
	return nil, fmt.Errorf("load Anime-Lists: remote: %v; cache: %v", fetchErr, cacheErr)
}

func readCache(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(path)
	return data, info, err
}

func (r *CachedAnimeListResolver) fetch(ctx context.Context) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.sourceURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/xml,text/xml")
	request.Header.Set("User-Agent", "ChineseSubFinder/Anime-Lists")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAnimeListSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAnimeListSize {
		return nil, errors.New("Anime-Lists document exceeds size limit")
	}
	return data, nil
}

func writeCacheAtomically(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
