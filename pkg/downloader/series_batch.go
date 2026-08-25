package downloader

import (
	"errors"
	"sort"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/sirupsen/logrus"
)

const seriesBatchSize = 4

func (d *Downloader) readySeriesBatch(primary taskQueueTypes.OneJob) []taskQueueTypes.OneJob {
	jobs := []taskQueueTypes.OneJob{primary}
	if primary.JobStatus != taskQueueTypes.Waiting || primary.SeriesRootDirPath == "" || primary.Season <= 0 {
		return jobs
	}
	companions := d.downloadQueue.GetReadySeriesJobs(primary.SeriesRootDirPath, primary.Season, primary.Id, seriesBatchSize-1, time.Now())
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
	}
	return out
}

type seriesIdentity struct {
	absoluteEpisode     int
	sceneSeason         int
	sceneEpisode        int
	numberingSource     string
	numberingConfidence float64
	seriesName          string
}

func (d *Downloader) completeSeriesBatch(jobs []taskQueueTypes.OneJob, saved map[string]struct{}, saveErrors map[string]error, fallback error) error {
	if fallback == nil {
		fallback = task_queue.ErrNoSubFound
	}
	primaryErr := fallback
	savedCount, errorCount := 0, 0
	for index, job := range jobs {
		key := pkg.GetEpisodeKeyName(job.Season, job.Episode)
		outcome := fallback
		if _, ok := saved[key]; ok {
			outcome = nil
			savedCount++
		} else if saveErr := saveErrors[key]; saveErr != nil {
			outcome = saveErr
		}
		if outcome != nil && !errors.Is(outcome, task_queue.ErrNoSubFound) {
			errorCount++
		}
		d.downloadQueue.AutoDetectUpdateJobStatus(job, outcome)
		if index == 0 {
			primaryErr = outcome
		}
	}
	d.log.WithFields(logrus.Fields{
		"event": "series_batch_complete", "batch_size": len(jobs), "saved": savedCount,
		"errors": errorCount, "no_subtitle": len(jobs) - savedCount - errorCount,
	}).Info("series batch completed")
	return primaryErr
}
