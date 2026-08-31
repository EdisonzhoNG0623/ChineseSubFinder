package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_parser/ass"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_parser/srt"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_parser_hub"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
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
	// SatisfiedVideoPaths is exact-path evidence. It must never be reduced to
	// SxxExx before matching against a concrete batch job because another cut of
	// the same episode may still be missing subtitles.
	SatisfiedVideoPaths map[string]struct{}
}

// backfillSeriesCollection fans a cached multi-episode archive out to every
// matching episode in the series. FileDownloader already persists the ASSRT
// archive by its stable result ID, so this path reuses that one download and
// never needs a second network fetch for another queued episode.
func (d *Downloader) backfillSeriesCollection(ctx context.Context, job taskQueue2.OneJob, batchJobs []taskQueue2.OneJob,
	organizeSubFiles map[string][]string) (collectionBackfillReport, error) {
	report := collectionBackfillReport{}
	targetKey := pkg.GetEpisodeKeyName(job.Season, job.Episode)
	if !hasAdditionalCollectionEpisodes(organizeSubFiles, targetKey) {
		return report, nil
	}

	seriesJobs := d.downloadQueue.GetSeriesJobs(job.SeriesRootDirPath)
	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		d.log, seriesJobs, organizeSubFiles, job.VideoFPath,
	)
	if err != nil {
		return report, fmt.Errorf("inspect series inventory for collection backfill: %w", err)
	}
	report.Available = len(candidates)
	report.SkippedExisting = skippedExisting
	report.SatisfiedVideoPaths = satisfiedVideoPaths

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
		if err = d.oneVideoSelectBestSubForCohort(
			episodeInfo.FileFullPath, organizeSubFiles[episodeKey], supplierMetricsCohort(job.VideoType),
		); err != nil {
			lastErr = err
			d.log.Warningf("Collection cache fan-out failed: series=%q episode=%s error=%v", filepath.Base(job.SeriesRootDirPath), episodeKey, err)
			continue
		}
		installed, verifyErr := videoHasValidChineseSubtitle(d.log, episodeInfo.FileFullPath)
		if verifyErr != nil {
			lastErr = verifyErr
			d.log.Warningf("Collection cache fan-out verification failed: series=%q episode=%s error=%v",
				filepath.Base(job.SeriesRootDirPath), episodeKey, verifyErr)
			continue
		}
		if !installed {
			lastErr = fmt.Errorf("collection backfill produced no valid Chinese subtitle for %s", episodeKey)
			d.log.Warningf("Collection cache fan-out verification failed: series=%q episode=%s error=%v",
				filepath.Base(job.SeriesRootDirPath), episodeKey, lastErr)
			continue
		}
		satisfiedVideoPaths[filepath.Clean(episodeInfo.FileFullPath)] = struct{}{}
		report.Saved++
	}

	activeBatchJobIDs := make([]string, 0, len(batchJobs))
	for _, batchJob := range batchJobs {
		activeBatchJobIDs = append(activeBatchJobIDs, batchJob.Id)
	}
	report.QueueMarked, err = d.downloadQueue.MarkSeriesEpisodesDone(job.SeriesRootDirPath,
		task_queue.NewVerifiedChineseVideoPaths(satisfiedVideoPaths), activeBatchJobIDs...)
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
	organizeSubFiles map[string][]string, targetVideoPath string) ([]series.EpisodeInfo, map[string]struct{}, int, error) {

	candidates := make([]series.EpisodeInfo, 0)
	satisfiedVideoPaths := make(map[string]struct{})
	seenVideoPaths := make(map[string]struct{})
	skippedExisting := 0
	verifiedVideoPaths, err := existingCollectionSubtitleIndex(log, jobs)
	if err != nil {
		return nil, nil, 0, err
	}
	cleanTargetVideoPath := ""
	if targetVideoPath != "" {
		cleanTargetVideoPath = filepath.Clean(targetVideoPath)
	}

	for _, seriesJob := range jobs {
		episodeKey := pkg.GetEpisodeKeyName(seriesJob.Season, seriesJob.Episode)
		cleanVideoPath := filepath.Clean(seriesJob.VideoFPath)
		if cleanVideoPath == cleanTargetVideoPath || len(organizeSubFiles[episodeKey]) == 0 {
			continue
		}
		usable, inspectErr := usableCollectionBackfillVideo(log, cleanVideoPath)
		if inspectErr != nil {
			return nil, nil, 0, inspectErr
		}
		if !usable {
			continue
		}
		if _, seen := seenVideoPaths[cleanVideoPath]; seen {
			continue
		}
		seenVideoPaths[cleanVideoPath] = struct{}{}

		if _, exists := verifiedVideoPaths[cleanVideoPath]; exists {
			skippedExisting++
			satisfiedVideoPaths[cleanVideoPath] = struct{}{}
			continue
		}
		candidates = append(candidates, series.EpisodeInfo{
			Title:        seriesJob.VideoName,
			Season:       seriesJob.Season,
			Episode:      seriesJob.Episode,
			FileFullPath: seriesJob.VideoFPath,
		})
	}

	return candidates, satisfiedVideoPaths, skippedExisting, nil
}

func existingCollectionSubtitleIndex(log *logrus.Logger, jobs []taskQueue2.OneJob) (map[string]struct{}, error) {
	verifiedVideoPaths := make(map[string]struct{})
	if len(jobs) == 0 {
		return verifiedVideoPaths, nil
	}

	// Scan only directories that contain queued videos, then strictly parse each
	// candidate. This binds evidence to a video basename and intentionally does
	// not use the general-purpose scanner's historical 1 KB size heuristic:
	// short but valid Chinese subtitles are still valid queue evidence.
	videosByDir := make(map[string][]string)
	seenVideoPaths := make(map[string]struct{})
	for _, job := range jobs {
		cleanVideoPath := filepath.Clean(job.VideoFPath)
		if _, seen := seenVideoPaths[cleanVideoPath]; seen {
			continue
		}
		seenVideoPaths[cleanVideoPath] = struct{}{}
		usable, err := usableCollectionBackfillVideo(log, cleanVideoPath)
		if err != nil {
			return nil, err
		}
		if !usable {
			continue
		}
		videoDir := filepath.Dir(cleanVideoPath)
		videosByDir[videoDir] = append(videosByDir[videoDir], cleanVideoPath)
	}
	parserHub := newCollectionSubtitleParser(log)
	for videoDir, queuedVideoPaths := range videosByDir {
		entries, err := os.ReadDir(videoDir)
		if os.IsNotExist(err) {
			log.WithField("video_dir", videoDir).Debug("skip stale collection backfill directory")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan existing subtitles under %s: %w", videoDir, err)
		}
		videoInventory := directoryVideoInventory(videoDir, entries, queuedVideoPaths)
		queuedPaths := make(map[string]struct{}, len(queuedVideoPaths))
		for _, videoPath := range queuedVideoPaths {
			queuedPaths[filepath.Clean(videoPath)] = struct{}{}
		}
		for _, entry := range entries {
			if entry.IsDir() || !sub_parser_hub.IsSubExtWanted(entry.Name()) {
				continue
			}
			subtitlePath := filepath.Join(videoDir, entry.Name())
			if !isValidChineseSubtitle(parserHub, subtitlePath) {
				continue
			}
			matchedVideoPath, matched := uniquelyMatchedVideoPath(videoInventory, entry.Name())
			if _, queued := queuedPaths[matchedVideoPath]; matched && queued {
				verifiedVideoPaths[matchedVideoPath] = struct{}{}
			}
		}
	}
	return verifiedVideoPaths, nil
}

func usableCollectionBackfillVideo(log *logrus.Logger, videoPath string) (bool, error) {
	info, err := os.Stat(videoPath)
	if err == nil {
		if info.Mode().IsRegular() {
			return true, nil
		}
		log.WithField("video_path", videoPath).Debug("skip non-regular collection backfill video")
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect collection backfill video %s: %w", videoPath, err)
	}
	// Blu-ray folders are represented by an intentionally nonexistent synthetic
	// video path. Preserve that established representation while filtering truly
	// stale queue entries.
	if isBluRay, _, _ := decode.IsFakeBDMVWorked(videoPath); isBluRay {
		return true, nil
	}
	log.WithField("video_path", videoPath).Debug("skip stale collection backfill video")
	return false, nil
}

func mergeBackfillBatchSuccesses(savedVideoPaths map[string]struct{}, batchJobs []taskQueue2.OneJob,
	satisfiedVideoPaths map[string]struct{}) {

	for _, batchJob := range batchJobs {
		videoPath := canonicalSeriesVideoPath(batchJob.VideoFPath)
		if _, satisfied := satisfiedVideoPaths[videoPath]; !satisfied {
			continue
		}
		savedVideoPaths[videoPath] = struct{}{}
	}
}

func videoHasValidChineseSubtitle(log *logrus.Logger, videoPath string) (bool, error) {
	videoDir := filepath.Dir(videoPath)
	entries, err := os.ReadDir(videoDir)
	if err != nil {
		return false, fmt.Errorf("scan subtitles for %s: %w", videoPath, err)
	}
	videoInventory := directoryVideoInventory(videoDir, entries, []string{videoPath})
	cleanVideoPath := filepath.Clean(videoPath)
	parserHub := newCollectionSubtitleParser(log)
	for _, entry := range entries {
		if entry.IsDir() || !sub_parser_hub.IsSubExtWanted(entry.Name()) {
			continue
		}
		ownerPath, ownership := exactVideoOwner(videoInventory, entry.Name())
		if ownership != exactVideoOwnershipUnique || ownerPath != cleanVideoPath {
			continue
		}
		subtitlePath := filepath.Join(videoDir, entry.Name())
		if isValidChineseSubtitle(parserHub, subtitlePath) {
			return true, nil
		}
	}
	return false, nil
}

func newCollectionSubtitleParser(log *logrus.Logger) *sub_parser_hub.SubParserHub {
	return sub_parser_hub.NewSubParserHub(log, ass.NewParser(log), srt.NewParser(log))
}

func isValidChineseSubtitle(parserHub *sub_parser_hub.SubParserHub, subtitlePath string) bool {
	found, info, err := parserHub.DetermineFileTypeFromFile(subtitlePath)
	if err != nil || !found || info == nil {
		return false
	}
	return parserHub.IsSubHasChinese(info)
}

var combinedEpisodeVideoStemPattern = regexp.MustCompile(`(?i)^(.+s\d+e\d+)-e\d+.*$`)

// uniquelyMatchedVideoPath associates an already parsed subtitle with one
// concrete queued video. Exact full-stem matches win. A legacy S01E01 subtitle
// may also serve an S01E01-E02 combined video, but only when that association is
// unique inside the directory; ambiguous multi-cut evidence is discarded.
func uniquelyMatchedVideoPath(videoPaths []string, subtitleName string) (string, bool) {
	if exactPath, ownership := exactVideoOwner(videoPaths, subtitleName); ownership != exactVideoOwnershipNone {
		return exactPath, ownership == exactVideoOwnershipUnique
	}
	type match struct {
		path  string
		score int
	}
	matches := make([]match, 0)
	lowerSubtitleName := strings.ToLower(subtitleName)
	for _, videoPath := range videoPaths {
		videoBase := strings.ToLower(filepath.Base(videoPath))
		videoStem := strings.TrimSuffix(videoBase, filepath.Ext(videoBase))
		combined := combinedEpisodeVideoStemPattern.FindStringSubmatch(videoStem)
		if len(combined) == 2 && strings.HasPrefix(lowerSubtitleName, combined[1]+".") {
			matches = append(matches, match{path: videoPath, score: len(combined[1])})
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	best := matches[0]
	ambiguous := false
	for _, candidate := range matches[1:] {
		switch {
		case candidate.score > best.score:
			best, ambiguous = candidate, false
		case candidate.score == best.score && candidate.path != best.path:
			ambiguous = true
		}
	}
	if ambiguous {
		return "", false
	}
	return filepath.Clean(best.path), true
}
