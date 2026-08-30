package series_helper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ai_ambiguity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/sirupsen/logrus"
)

var processAnimeResolver struct {
	once     sync.Once
	resolver episode_identity.Resolver
	err      error
}

func enrichSeriesEpisodeNumbering(logger *logrus.Logger, seriesInfo *series.SeriesInfo) error {
	if seriesInfo == nil || len(seriesInfo.NeedDlEpsKeyList) == 0 {
		return nil
	}
	// Anime-Lists cannot classify an ordinary series without any searchable
	// title or external ID. Avoid loading the remote mapping in that case; this
	// also keeps supplier dispatch independent from an irrelevant network call.
	if !seriesInfo.IsAnime && !hasAnimeLookupIdentity(seriesInfo) {
		return nil
	}
	resolver, err := defaultAnimeEpisodeResolver()
	if err != nil {
		fallbackCount := 0
		if seriesInfo.IsAnime {
			fallbackCount = applyContiguousInventoryAbsoluteFallback(seriesInfo)
		}
		if fallbackCount > 0 {
			logger.Infof("resolved absolute episode numbering for %d episode(s) via contiguous local inventory", fallbackCount)
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	matchedAnime, matchErr := detectAnimeSeries(ctx, resolver, seriesInfo)
	if matchErr != nil {
		return matchErr
	}
	if !matchedAnime {
		return nil
	}
	if !seriesInfo.IsAnime {
		seriesInfo.IsAnime = true
		logger.Infof("series classified as anime via Anime-Lists metadata")
	}
	resolved, resolveErr := enrichSeriesEpisodeNumberingWithResolvers(ctx, resolver, ai_ambiguity.ConfiguredResolver(), seriesInfo)
	if resolved > 0 {
		logger.Infof("resolved absolute episode numbering for %d episode(s) via Anime-Lists", resolved)
	}
	fallbackCount := applyContiguousInventoryAbsoluteFallback(seriesInfo)
	if fallbackCount > 0 {
		logger.Infof("resolved absolute episode numbering for %d episode(s) via contiguous local inventory", fallbackCount)
	}
	if resolveErr != nil && fallbackCount == 0 {
		return resolveErr
	}
	return nil
}

func hasAnimeLookupIdentity(seriesInfo *series.SeriesInfo) bool {
	if seriesInfo == nil {
		return false
	}
	if strings.TrimSpace(seriesInfo.ImdbId) != "" || strings.TrimSpace(seriesInfo.TmdbId) != "" ||
		strings.TrimSpace(seriesInfo.TvdbId) != "" || strings.TrimSpace(seriesInfo.Name) != "" {
		return true
	}
	for _, alias := range seriesInfo.Aliases {
		if strings.TrimSpace(alias) != "" {
			return true
		}
	}
	return false
}

func detectAnimeSeries(ctx context.Context, resolver episode_identity.Resolver, seriesInfo *series.SeriesInfo) (bool, error) {
	if seriesInfo == nil {
		return false, nil
	}
	if seriesInfo.IsAnime {
		return true, nil
	}
	matcher, ok := resolver.(episode_identity.SeriesMatcher)
	if !ok {
		return false, nil
	}
	request := episode_identity.Request{
		IDs: episode_identity.ExternalIDs{
			IMDb: seriesInfo.ImdbId, TMDB: seriesInfo.TmdbId, TVDB: seriesInfo.TvdbId,
		},
		SeriesName: seriesInfo.Name,
		Aliases:    seriesInfo.Aliases,
	}
	return matcher.MatchesSeries(ctx, request)
}

// applyContiguousInventoryAbsoluteFallback derives absolute anime numbering
// only when every positive local season and every episode inside it is
// contiguous from one. A gap makes the cumulative offset unsafe, so the whole
// fallback abstains. Existing resolver evidence always wins.
func applyContiguousInventoryAbsoluteFallback(seriesInfo *series.SeriesInfo) int {
	if seriesInfo == nil || !seriesInfo.IsAnime {
		return 0
	}
	inventory := seriesInfo.ArchiveEpList
	if len(inventory) == 0 {
		inventory = seriesInfo.EpList
	}
	bySeason := make(map[int]map[int]struct{})
	maxSeason := 0
	for _, episode := range inventory {
		if episode.Season <= 0 || episode.Episode <= 0 {
			continue
		}
		if bySeason[episode.Season] == nil {
			bySeason[episode.Season] = make(map[int]struct{})
		}
		bySeason[episode.Season][episode.Episode] = struct{}{}
		if episode.Season > maxSeason {
			maxSeason = episode.Season
		}
	}
	if len(bySeason) < 2 || maxSeason < 2 {
		return 0
	}

	offsetBySeason := make(map[int]int, maxSeason)
	cumulative := 0
	for season := 1; season <= maxSeason; season++ {
		episodes := bySeason[season]
		if len(episodes) == 0 {
			return 0
		}
		maxEpisode := 0
		for episode := range episodes {
			if episode > maxEpisode {
				maxEpisode = episode
			}
		}
		if maxEpisode != len(episodes) {
			return 0
		}
		for episode := 1; episode <= maxEpisode; episode++ {
			if _, exists := episodes[episode]; !exists {
				return 0
			}
		}
		offsetBySeason[season] = cumulative
		cumulative += maxEpisode
	}

	apply := func(episode *series.EpisodeInfo) bool {
		// A later local season is evidence that this season's boundary is
		// complete. Do not guess offsets for the newest/only season.
		if episode == nil || episode.AbsoluteEpisode > 0 || episode.Season <= 1 ||
			episode.Season >= maxSeason || episode.Episode <= 0 {
			return false
		}
		offset, exists := offsetBySeason[episode.Season]
		if !exists {
			return false
		}
		episode.AbsoluteEpisode = offset + episode.Episode
		episode.NumberingSource = "local contiguous season inventory"
		episode.NumberingConfidence = 0.8
		return true
	}
	for index := range seriesInfo.ArchiveEpList {
		apply(&seriesInfo.ArchiveEpList[index])
	}
	for index := range seriesInfo.EpList {
		apply(&seriesInfo.EpList[index])
	}
	resolved := 0
	for key, episode := range seriesInfo.NeedDlEpsKeyList {
		if apply(&episode) {
			resolved++
			seriesInfo.NeedDlEpsKeyList[key] = episode
		}
	}
	return resolved
}

func defaultAnimeEpisodeResolver() (episode_identity.Resolver, error) {
	processAnimeResolver.once.Do(func() {
		cacheRoot, err := pkg.GetRootCacheCenterFolder()
		if err != nil {
			processAnimeResolver.err = err
			return
		}
		client, err := pkg.NewHttpClient()
		if err != nil {
			processAnimeResolver.err = err
			return
		}
		client.SetTimeout(15 * time.Second).SetRetryCount(1)
		processAnimeResolver.resolver = episode_identity.NewCachedAnimeListResolver(
			client.GetClient(), filepath.Join(cacheRoot, "anime-list.xml"), 7*24*time.Hour,
		)
	})
	return processAnimeResolver.resolver, processAnimeResolver.err
}

// EnrichSeriesEpisodeNumbering resolves all requested episodes before
// suppliers start concurrently, avoiding shared-map writes in supplier
// goroutines. Missing mappings are expected and leave ordinary TV unchanged.
func EnrichSeriesEpisodeNumbering(ctx context.Context, resolver episode_identity.Resolver, seriesInfo *series.SeriesInfo) (int, error) {
	return enrichSeriesEpisodeNumberingWithResolvers(ctx, resolver, episode_identity.DisabledAmbiguityResolver{}, seriesInfo)
}

func enrichSeriesEpisodeNumberingWithResolvers(ctx context.Context, resolver episode_identity.Resolver,
	ambiguityResolver episode_identity.AmbiguityResolver, seriesInfo *series.SeriesInfo) (int, error) {
	if resolver == nil || seriesInfo == nil {
		return 0, nil
	}
	resolvedCount := 0
	for key, episode := range seriesInfo.NeedDlEpsKeyList {
		request := episode_identity.Request{
			IDs: episode_identity.ExternalIDs{
				IMDb: seriesInfo.ImdbId, TMDB: seriesInfo.TmdbId, TVDB: seriesInfo.TvdbId,
			},
			SeriesName: seriesInfo.Name, Aliases: seriesInfo.Aliases, Season: episode.Season, Episode: episode.Episode,
			EpisodeTitle: episode.Title, AirDate: episode.AiredTime, FileName: filepath.Base(episode.FileFullPath),
		}
		identity, err := resolveEpisodeIdentity(ctx, resolver, ambiguityResolver, request)
		if errors.Is(err, episode_identity.ErrNoMapping) {
			continue
		}
		if err != nil {
			return resolvedCount, err
		}
		episode.AbsoluteEpisode = identity.AbsoluteEpisode
		episode.SceneSeason = identity.SceneSeason
		episode.SceneEpisode = identity.SceneEpisode
		episode.NumberingConfidence = identity.Confidence
		if len(identity.Evidence) > 0 {
			episode.NumberingSource = identity.Evidence[0].Source
			for _, evidence := range identity.Evidence[1:] {
				if evidence.Source == "AI ambiguity" {
					episode.NumberingSource += " + AI ambiguity"
					break
				}
			}
		}
		seriesInfo.NeedDlEpsKeyList[key] = episode
		for index := range seriesInfo.EpList {
			if seriesInfo.EpList[index].Season == episode.Season && seriesInfo.EpList[index].Episode == episode.Episode {
				seriesInfo.EpList[index] = episode
			}
		}
		resolvedCount++
	}
	return resolvedCount, nil
}

func resolveEpisodeIdentity(ctx context.Context, resolver episode_identity.Resolver,
	ambiguityResolver episode_identity.AmbiguityResolver, request episode_identity.Request) (episode_identity.Identity, error) {
	candidateResolver, ok := resolver.(episode_identity.CandidateResolver)
	if !ok {
		return resolver.Resolve(ctx, request)
	}
	candidates, err := candidateResolver.ResolveCandidates(ctx, request)
	if err != nil {
		return episode_identity.Identity{}, err
	}
	if len(candidates) == 0 {
		return episode_identity.Identity{}, episode_identity.ErrNoMapping
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if ambiguityResolver == nil {
		return episode_identity.Identity{}, episode_identity.ErrNoMapping
	}
	if len(candidates) > 8 {
		candidates = candidates[:8]
	}
	facts := make([]episode_identity.CandidateFact, 0, len(candidates))
	for index, candidate := range candidates {
		evidence := make([]string, 0, len(candidate.Evidence))
		for _, item := range candidate.Evidence {
			evidence = append(evidence, fmt.Sprintf("%s:%.2f", item.Source, item.Confidence))
		}
		facts = append(facts, episode_identity.CandidateFact{
			CandidateID: fmt.Sprintf("identity-%d", index), Supplier: "Anime-Lists", Name: request.SeriesName,
			Season: candidate.Season, Episode: candidate.Episode, AbsoluteEpisode: candidate.AbsoluteEpisode,
			DeterministicScore: int64(candidate.Confidence * 1000), Evidence: evidence,
		})
	}
	media := request
	media.FileName = ""
	ambiguityRequest := episode_identity.AmbiguityRequest{
		SchemaVersion: episode_identity.AmbiguitySchemaVersion, Media: media, Candidates: facts,
	}
	result, err := ambiguityResolver.ResolveAmbiguity(ctx, ambiguityRequest)
	if err != nil || result.Decision != episode_identity.AmbiguityMatch {
		return episode_identity.Identity{}, episode_identity.ErrNoMapping
	}
	if episode_identity.ValidateAmbiguityResult(ambiguityRequest, result) != nil || result.Confidence < 0.85 {
		return episode_identity.Identity{}, episode_identity.ErrNoMapping
	}
	for index := range facts {
		if facts[index].CandidateID == result.CandidateID {
			selected := candidates[index]
			if result.Confidence < selected.Confidence {
				selected.Confidence = result.Confidence
			}
			selected.Evidence = append(selected.Evidence, episode_identity.Evidence{
				Source: "AI ambiguity", Rule: result.Model, Confidence: result.Confidence,
			})
			return selected, nil
		}
	}
	return episode_identity.Identity{}, episode_identity.ErrNoMapping
}
