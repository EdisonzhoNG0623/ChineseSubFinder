package downloader

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type collectionBackfillReport struct {
	Available       int
	SkippedExisting int
	Saved           int
	QueueMarked     int
}

// backfillSeriesCollection fans a cached multi-episode archive out to every
// matching episode in the series. FileDownloader already persists the ASSRT
// archive by its stable result ID, so this path reuses that one download and
// never needs a second network fetch for another queued episode.
func (d *Downloader) backfillSeriesCollection(ctx context.Context, job taskQueue2.OneJob, organizeSubFiles map[string][]string) (collectionBackfillReport, error) {
	report := collectionBackfillReport{}
	targetKey := pkg.GetEpisodeKeyName(job.Season, job.Episode)
	if !hasAdditionalCollectionEpisodes(organizeSubFiles, targetKey) {
		return report, nil
	}

	seriesJobs := d.downloadQueue.GetSeriesJobs(job.SeriesRootDirPath)
	candidates, satisfiedEpisodeKeys, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		d.log, seriesJobs, organizeSubFiles, targetKey,
	)
	if err != nil {
		return report, fmt.Errorf("inspect series inventory for collection backfill: %w", err)
	}
	report.Available = len(candidates)
	report.SkippedExisting = skippedExisting

	d.log.Infof("Collection cache fan-out start: series=%q cached_episodes=%d candidates=%d skipped_existing=%d",
		filepath.Base(job.SeriesRootDirPath), countCollectionEpisodes(organizeSubFiles), len(candidates), skippedExisting)

	var lastErr error
	for _, episodeInfo := range candidates {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}

		episodeKey := pkg.GetEpisodeKeyName(episodeInfo.Season, episodeInfo.Episode)
		if err = d.oneVideoSelectBestSub(episodeInfo.FileFullPath, organizeSubFiles[episodeKey]); err != nil {
			lastErr = err
			d.log.Warningf("Collection cache fan-out failed: series=%q episode=%s error=%v", filepath.Base(job.SeriesRootDirPath), episodeKey, err)
			continue
		}
		satisfiedEpisodeKeys[episodeKey] = struct{}{}
		report.Saved++
	}

	report.QueueMarked, err = d.downloadQueue.MarkSeriesEpisodesDone(job.SeriesRootDirPath, satisfiedEpisodeKeys, job.Id)
	if err != nil {
		return report, fmt.Errorf("mark collection-backfilled queue jobs done: %w", err)
	}
	d.log.Infof("Collection cache fan-out complete: series=%q saved=%d queue_marked=%d skipped_existing=%d",
		filepath.Base(job.SeriesRootDirPath), report.Saved, report.QueueMarked, report.SkippedExisting)

	return report, lastErr
}

func hasAdditionalCollectionEpisodes(organizeSubFiles map[string][]string, targetKey string) bool {
	for episodeKey, files := range organizeSubFiles {
		if episodeKey != targetKey && len(files) > 0 {
			return true
		}
	}
	return false
}

func countCollectionEpisodes(organizeSubFiles map[string][]string) int {
	count := 0
	for _, files := range organizeSubFiles {
		if len(files) > 0 {
			count++
		}
	}
	return count
}

func collectionBackfillCandidatesFromJobs(log *logrus.Logger, jobs []taskQueue2.OneJob,
	organizeSubFiles map[string][]string, targetKey string) ([]series.EpisodeInfo, map[string]struct{}, int, error) {

	candidates := make([]series.EpisodeInfo, 0)
	satisfiedEpisodeKeys := make(map[string]struct{})
	seenEpisodeKeys := make(map[string]struct{})
	skippedExisting := 0
	existingEpisodeKeys, subtitleNamesByDir, err := existingCollectionSubtitleIndex(log, jobs)
	if err != nil {
		return nil, nil, 0, err
	}

	for _, seriesJob := range jobs {
		episodeKey := pkg.GetEpisodeKeyName(seriesJob.Season, seriesJob.Episode)
		if episodeKey == targetKey || len(organizeSubFiles[episodeKey]) == 0 {
			continue
		}
		if _, seen := seenEpisodeKeys[episodeKey]; seen {
			continue
		}
		seenEpisodeKeys[episodeKey] = struct{}{}

		if _, exists := existingEpisodeKeys[episodeKey]; exists || videoHasIndexedSubtitle(seriesJob.VideoFPath, subtitleNamesByDir) {
			skippedExisting++
			satisfiedEpisodeKeys[episodeKey] = struct{}{}
			continue
		}
		candidates = append(candidates, series.EpisodeInfo{
			Title:        seriesJob.VideoName,
			Season:       seriesJob.Season,
			Episode:      seriesJob.Episode,
			FileFullPath: seriesJob.VideoFPath,
		})
	}

	return candidates, satisfiedEpisodeKeys, skippedExisting, nil
}

func existingCollectionSubtitleIndex(log *logrus.Logger, jobs []taskQueue2.OneJob) (map[string]struct{}, map[string][]string, error) {
	episodeKeys := make(map[string]struct{})
	subtitleNamesByDir := make(map[string][]string)
	if len(jobs) == 0 {
		return episodeKeys, subtitleNamesByDir, nil
	}

	subtitlePaths, err := sub_helper.SearchMatchedSubFileByDir(log, jobs[0].SeriesRootDirPath)
	if err != nil {
		return nil, nil, fmt.Errorf("scan existing subtitles under %s: %w", jobs[0].SeriesRootDirPath, err)
	}
	for _, subtitlePath := range subtitlePaths {
		subtitleNamesByDir[filepath.Dir(subtitlePath)] = append(
			subtitleNamesByDir[filepath.Dir(subtitlePath)], strings.ToLower(filepath.Base(subtitlePath)),
		)
		_, season, episode, err := decode.GetSeasonAndEpisodeFromSubFileName(filepath.Base(subtitlePath))
		if err == nil && season > 0 && episode > 0 {
			episodeKeys[pkg.GetEpisodeKeyName(season, episode)] = struct{}{}
		}
	}
	return episodeKeys, subtitleNamesByDir, nil
}

func videoHasIndexedSubtitle(videoPath string, subtitleNamesByDir map[string][]string) bool {
	videoStem := strings.TrimSuffix(strings.ToLower(filepath.Base(videoPath)), strings.ToLower(filepath.Ext(videoPath))) + "."
	for _, subtitleName := range subtitleNamesByDir[filepath.Dir(videoPath)] {
		if strings.HasPrefix(subtitleName, videoStem) {
			return true
		}
	}
	return false
}
