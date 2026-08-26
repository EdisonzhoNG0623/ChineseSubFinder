package series_helper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	resolver, err := defaultAnimeEpisodeResolver()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resolved, err := enrichSeriesEpisodeNumberingWithResolvers(ctx, resolver, ai_ambiguity.ConfiguredResolver(), seriesInfo)
	if err != nil {
		return err
	}
	if resolved > 0 {
		logger.Infof("resolved absolute episode numbering for %d episode(s) via Anime-Lists", resolved)
	}
	return nil
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
