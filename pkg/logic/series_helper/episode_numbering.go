package series_helper

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
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
	if seriesInfo.TmdbId == "" && seriesInfo.TvdbId == "" && seriesInfo.ImdbId == "" {
		return nil
	}
	resolver, err := defaultAnimeEpisodeResolver()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resolved, err := EnrichSeriesEpisodeNumbering(ctx, resolver, seriesInfo)
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
	if resolver == nil || seriesInfo == nil {
		return 0, nil
	}
	resolvedCount := 0
	for key, episode := range seriesInfo.NeedDlEpsKeyList {
		identity, err := resolver.Resolve(ctx, episode_identity.Request{
			IDs: episode_identity.ExternalIDs{
				IMDb: seriesInfo.ImdbId, TMDB: seriesInfo.TmdbId, TVDB: seriesInfo.TvdbId,
			},
			SeriesName: seriesInfo.Name, Season: episode.Season, Episode: episode.Episode,
			EpisodeTitle: episode.Title, AirDate: episode.AiredTime, FileName: filepath.Base(episode.FileFullPath),
		})
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
