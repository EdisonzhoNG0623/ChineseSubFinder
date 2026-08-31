package downloader

import (
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/sirupsen/logrus"
)

func mergeSeriesSearchAliases(seriesInfo *series.SeriesInfo, values ...string) {
	if seriesInfo == nil {
		return
	}
	aliases := append([]string{seriesInfo.Name}, seriesInfo.Aliases...)
	aliases = append(aliases, values...)
	seriesInfo.Aliases = taskQueueTypes.NormalizeSearchAliases(aliases...)
}

const (
	newSeriesBatchSize     = 4
	retrySeriesBatchSize   = 8
	historySeriesBatchSize = 12
)

func (d *Downloader) readySeriesBatch(primary taskQueueTypes.OneJob) []taskQueueTypes.OneJob {
	jobs := []taskQueueTypes.OneJob{primary}
	if primary.JobStatus != taskQueueTypes.Waiting || primary.SeriesRootDirPath == "" || primary.Season <= 0 {
		return jobs
	}
	limit := seriesBatchLimit(primary)
	companions := d.downloadQueue.GetReadySeriesJobs(primary.SeriesRootDirPath, primary.Season, primary.Id, limit-1, time.Now())
	seenEpisodes := map[string]struct{}{pkg.GetEpisodeKeyName(primary.Season, primary.Episode): {}}
	for _, companion := range companions {
		key := pkg.GetEpisodeKeyName(companion.Season, companion.Episode)
		if _, duplicate := seenEpisodes[key]; duplicate {
			continue
		}
		isBluRay, _, _ := decode.IsFakeBDMVWorked(companion.VideoFPath)
		if !isBluRay && !pkg.IsFile(companion.VideoFPath) {
			continue
		}
		seenEpisodes[key] = struct{}{}
		jobs = append(jobs, companion)
	}
	return jobs
}

func seriesBatchLimit(primary taskQueueTypes.OneJob) int {
	switch {
	case primary.DownloadTimes >= 3:
		return historySeriesBatchSize
	case primary.DownloadTimes > 0:
		return retrySeriesBatchSize
	default:
		return newSeriesBatchSize
	}
}

func buildSeriesEpisodeMap(jobs []taskQueueTypes.OneJob) map[int][]int {
	bySeason := make(map[int][]int)
	seen := make(map[string]struct{})
	for _, job := range jobs {
		if job.Season <= 0 || job.Episode <= 0 {
			continue
		}
		key := pkg.GetEpisodeKeyName(job.Season, job.Episode)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		bySeason[job.Season] = append(bySeason[job.Season], job.Episode)
	}
	for season := range bySeason {
		sort.Ints(bySeason[season])
	}
	return bySeason
}

// bindSeriesInfoToClaimedJobs keeps SxxExx as the supplier search key while
// restoring the concrete video identity selected by the queue. The directory
// scanner intentionally collapses duplicate cuts to one EpisodeInfo, and its
// traversal order must not decide where a claimed job writes subtitles.
func bindSeriesInfoToClaimedJobs(seriesInfo *series.SeriesInfo, jobs []taskQueueTypes.OneJob) {
	if seriesInfo == nil || len(jobs) == 0 {
		return
	}
	jobsByEpisode := make(map[string]taskQueueTypes.OneJob, len(jobs))
	for _, job := range jobs {
		if job.VideoFPath == "" {
			continue
		}
		episodeKey := pkg.GetEpisodeKeyName(job.Season, job.Episode)
		if _, alreadyBound := jobsByEpisode[episodeKey]; alreadyBound {
			// readySeriesBatch enforces this invariant. First-wins keeps direct
			// callers conservative if they nevertheless pass duplicate cuts.
			continue
		}
		jobsByEpisode[episodeKey] = job
	}
	bind := func(episode series.EpisodeInfo, job taskQueueTypes.OneJob) series.EpisodeInfo {
		episode.Season = job.Season
		episode.Episode = job.Episode
		episode.FileFullPath = job.VideoFPath
		episode.Dir = filepath.Dir(job.VideoFPath)
		episode.MediaServerInsideVideoID = job.MediaServerInsideVideoID
		if job.AbsoluteEpisode > 0 {
			episode.AbsoluteEpisode = job.AbsoluteEpisode
		}
		if job.SceneSeason > 0 && job.SceneEpisode > 0 {
			episode.SceneSeason = job.SceneSeason
			episode.SceneEpisode = job.SceneEpisode
		}
		if job.NumberingSource != "" {
			episode.NumberingSource = job.NumberingSource
			episode.NumberingConfidence = job.NumberingConfidence
		}
		if job.VideoName != "" {
			episode.Title = job.VideoName
		} else {
			episode.Title = filepath.Base(job.VideoFPath)
		}
		// ReadSeriesInfoFromDir may have copied subtitles from an alternate cut.
		// A claimed job is explicitly requesting this exact video, so that evidence
		// must not follow the collapsed EpisodeInfo to the target path.
		episode.SubAlreadyDownloadedList = nil
		return episode
	}
	boundNeed := make(map[string]struct{}, len(seriesInfo.NeedDlEpsKeyList))
	for episodeKey, episode := range seriesInfo.NeedDlEpsKeyList {
		if job, found := jobsByEpisode[episodeKey]; found {
			seriesInfo.NeedDlEpsKeyList[episodeKey] = bind(episode, job)
			boundNeed[episodeKey] = struct{}{}
		}
	}
	boundEpisodes := make(map[string]struct{}, len(seriesInfo.EpList))
	for index, episode := range seriesInfo.EpList {
		episodeKey := pkg.GetEpisodeKeyName(episode.Season, episode.Episode)
		if job, found := jobsByEpisode[episodeKey]; found {
			seriesInfo.EpList[index] = bind(episode, job)
			boundEpisodes[episodeKey] = struct{}{}
		}
	}
	archiveKeys := make(map[string]struct{}, len(seriesInfo.ArchiveEpList))
	for _, episode := range seriesInfo.ArchiveEpList {
		archiveKeys[pkg.GetEpisodeKeyName(episode.Season, episode.Episode)] = struct{}{}
	}
	if seriesInfo.NeedDlEpsKeyList == nil {
		seriesInfo.NeedDlEpsKeyList = make(map[string]series.EpisodeInfo)
	}
	if seriesInfo.NeedDlSeasonDict == nil {
		seriesInfo.NeedDlSeasonDict = make(map[int]int)
	}
	if seriesInfo.SeasonDict == nil {
		seriesInfo.SeasonDict = make(map[int]int)
	}
	for episodeKey, job := range jobsByEpisode {
		if job.Season <= 0 || job.Episode <= 0 {
			continue
		}
		bound := bind(series.EpisodeInfo{}, job)
		if _, found := boundNeed[episodeKey]; !found {
			seriesInfo.NeedDlEpsKeyList[episodeKey] = bound
		}
		if _, found := boundEpisodes[episodeKey]; !found {
			seriesInfo.EpList = append(seriesInfo.EpList, bound)
		}
		if _, found := archiveKeys[episodeKey]; !found {
			// ArchiveEpList remains search-only. Adding the exact claimed path for a
			// missing scan entry makes collection mapping aware of the episode, while
			// outcome evidence continues to come only from exact-path save maps.
			seriesInfo.ArchiveEpList = append(seriesInfo.ArchiveEpList, bound)
		}
		seriesInfo.NeedDlSeasonDict[job.Season] = job.Season
		seriesInfo.SeasonDict[job.Season] = job.Season
	}
}

func enrichSeriesBatchJobs(jobs []taskQueueTypes.OneJob, identities map[string]seriesIdentity) []taskQueueTypes.OneJob {
	out := append([]taskQueueTypes.OneJob(nil), jobs...)
	for index := range out {
		identity, found := identities[pkg.GetEpisodeKeyName(out[index].Season, out[index].Episode)]
		if !found {
			continue
		}
		out[index].AbsoluteEpisode = identity.absoluteEpisode
		out[index].SceneSeason = identity.sceneSeason
		out[index].SceneEpisode = identity.sceneEpisode
		out[index].NumberingSource = identity.numberingSource
		out[index].NumberingConfidence = identity.numberingConfidence
		out[index].SeriesName = identity.seriesName
		out[index].SearchAliases = append([]string(nil), identity.aliases...)
		out[index].RefreshSearchFingerprint()
	}
	return out
}

func seriesSearchFingerprint(job taskQueueTypes.OneJob, identity seriesIdentity) string {
	return taskQueueTypes.SearchEvidenceFingerprintWithAliases(identity.seriesName, identity.aliases, job.Season, job.Episode,
		identity.absoluteEpisode, identity.sceneSeason, identity.sceneEpisode, identity.numberingSource)
}

type seriesIdentity struct {
	absoluteEpisode     int
	sceneSeason         int
	sceneEpisode        int
	numberingSource     string
	numberingConfidence float64
	seriesName          string
	aliases             []string
}

func canonicalSeriesVideoPath(videoPath string) string {
	if videoPath == "" {
		return ""
	}
	return filepath.Clean(videoPath)
}

func recordSeriesSaveResult(savedVideoPaths map[string]struct{}, saveErrorsByVideoPath map[string]error,
	videoPath string, saveErr error) {

	videoPath = canonicalSeriesVideoPath(videoPath)
	if videoPath == "" {
		return
	}
	if saveErr != nil {
		saveErrorsByVideoPath[videoPath] = saveErr
		return
	}
	savedVideoPaths[videoPath] = struct{}{}
}

func seriesBatchJobOutcome(job taskQueueTypes.OneJob, savedVideoPaths map[string]struct{},
	saveErrorsByVideoPath map[string]error, fallback error) error {

	videoPath := canonicalSeriesVideoPath(job.VideoFPath)
	if _, saved := savedVideoPaths[videoPath]; saved {
		return nil
	}
	if saveErr := saveErrorsByVideoPath[videoPath]; saveErr != nil {
		return saveErr
	}
	return fallback
}

func (d *Downloader) completeSeriesBatch(jobs []taskQueueTypes.OneJob, savedVideoPaths map[string]struct{},
	saveErrorsByVideoPath map[string]error, fallback error) error {
	if d.ctx != nil && d.ctx.Err() != nil {
		return d.ctx.Err()
	}
	if fallback == nil {
		fallback = task_queue.ErrNoSubFound
	}
	primaryErr := fallback
	savedCount, errorCount := 0, 0
	outcomes := make([]task_queue.JobOutcome, 0, len(jobs))
	for index, job := range jobs {
		outcome := seriesBatchJobOutcome(job, savedVideoPaths, saveErrorsByVideoPath, fallback)
		if outcome == nil {
			savedCount++
		}
		if outcome != nil && !errors.Is(outcome, task_queue.ErrNoSubFound) {
			errorCount++
		}
		outcomes = append(outcomes, task_queue.JobOutcome{Job: job, Err: outcome})
		if index == 0 {
			primaryErr = outcome
		}
	}
	if err := d.downloadQueue.ApplyOutcomesReliable(outcomes); err != nil {
		d.log.WithError(err).Error("persist series batch outcomes")
		if primaryErr == nil {
			primaryErr = err
		}
	}
	d.log.WithFields(logrus.Fields{
		"event": "series_batch_complete", "batch_size": len(jobs), "saved": savedCount,
		"errors": errorCount, "no_subtitle": len(jobs) - savedCount - errorCount,
	}).Info("series batch completed")
	return primaryErr
}
