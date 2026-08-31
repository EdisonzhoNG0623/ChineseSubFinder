package downloader

import (
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	common2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"golang.org/x/net/context"
)

type queueWorkerResult struct {
	err        error
	panicValue interface{}
	stack      []byte
}

// waitQueueWorkerResult releases administrative ownership promptly on daemon
// cancellation, but does not let the outer worker return until the actual job
// goroutine has stopped. Shared browser and temporary-file cleanup therefore
// cannot overlap a non-context-aware subtitle save.
func waitQueueWorkerResult(ctx context.Context, done <-chan queueWorkerResult, onCancel func()) (queueWorkerResult, bool) {
	select {
	case result := <-done:
		return result, false
	case <-ctx.Done():
		if onCancel != nil {
			onCancel()
		}
		return <-done, true
	}
}

func shouldIgnoreSeriesBeforeClaim(bNeedDlSub bool) bool {
	return !bNeedDlSub
}

func (d *Downloader) queueDownloaderLocal() {

	d.log.Debugln("Download.QueueDownloader() Try Start ...")
	d.queueMaintenanceLock.RLock()
	defer d.queueMaintenanceLock.RUnlock()
	didWork := false
	defer func() { d.finishQueueWorker(didWork) }()

	d.queueClaimLock.Lock()
	claimLockHeld := true
	defer func() {
		if claimLockHeld {
			d.queueClaimLock.Unlock()
		}
	}()
	d.log.Debugln("Download.QueueDownloader() Start ...")

	defer func() {
		if p := recover(); p != nil {
			d.log.Errorln("Downloader.QueueDownloader() panic")
			pkg.PrintPanicStack(d.log)
		}
		d.log.Debugln("Download.QueueDownloader() End")
	}()

	nowSubSupplierHub := d.getSubSupplierHub()
	if nowSubSupplierHub == nil {
		d.log.Debugln("Download.QueueDownloader() supplier hub is not ready")
		return
	}
	if len(nowSubSupplierHub.Suppliers) == 0 {
		d.log.Debugln("Download.QueueDownloader() has no active suppliers")
		return
	}
	// 移除查过三个月的 Done 任务
	d.downloadQueue.BeforeGetOneJob()
	// 从队列取数据出来，见《任务生命周期》
	bok, oneJob, err := d.downloadQueue.GetOneJobExcludingSeries(d.activeSeriesSnapshot())
	if err != nil {
		d.log.Errorln("d.downloadQueue.GetOneWaitingJob()", err)
		return
	}
	if bok == false {
		d.log.Debugln("Download Queue Is Empty, Skip This Time")
		return
	}
	// --------------------------------------------------
	{
		// 需要判断这个任务是否需要跳过，但是如果这个任务的优先级很高，那么就不跳过
		// 正常任务是 5，插队任务是3，一次性任务是 0.
		if oneJob.TaskPriority > task_queue.HighTaskPriorityLevel {
			// 说明优先级不高，需要进行判断
			videoType := 0
			if oneJob.VideoType != common2.Movie {
				videoType = 1
			}
			if d.ScanLogic.Get(videoType, oneJob.VideoFPath) == true {
				// 需要标记忽略
				markJobIgnored(&oneJob)
				bok, err = d.downloadQueue.Update(oneJob)
				if err != nil {
					d.log.Errorln("d.downloadQueue.Update()", err)
					return
				}
				if bok == false {
					d.log.Errorln("d.downloadQueue.Update() Failed")
					return
				}
				d.log.Infoln("Download Queue Update Job Status To Ignore (Manual Settings Ignore), VideoFPath:", oneJob.VideoFPath)
				return
			}
		}
	}
	// --------------------------------------------------
	// Missing files must be removed before metadata repair. Otherwise a stale
	// series job without a reachable tvshow.nfo is retained forever as a local
	// metadata failure.
	{
		isBlue, _, _ := decode.IsFakeBDMVWorked(oneJob.VideoFPath)
		if isBlue == false && pkg.IsFile(oneJob.VideoFPath) == false {
			bok, err = d.downloadQueue.Del(oneJob.Id)
			if err != nil {
				d.log.Errorln("d.downloadQueue.Del()", err)
				return
			}
			if bok == false {
				d.log.Errorln(fmt.Sprintf("d.downloadQueue.Del(%s) == false", oneJob.Id))
				return
			}
			d.log.Infoln(oneJob.VideoFPath, "is missing, Delete This Job")
			return
		}
	}
	// --------------------------------------------------
	// Repair legacy series metadata before any supplier call. The nearest
	// scraped root is authoritative even when an old queue item already has a
	// non-empty (but overly broad) category directory persisted.
	if oneJob.VideoType != common2.Movie {
		oldRoot := oneJob.SeriesRootDirPath
		oldSeason, oldEpisode := oneJob.Season, oneJob.Episode
		if oneJob.Season <= 0 || oneJob.Episode <= 0 {
			season, episode, source, metadataErr := resolveLegacySeriesEpisode(oneJob.VideoFPath)
			if metadataErr != nil {
				blockedErr := fmt.Errorf("series metadata episode not found")
				d.log.WithFields(map[string]interface{}{
					"event": "series_identity_blocked", "reason": "episode_metadata_missing", "job_id": oneJob.Id,
				}).Warn(blockedErr)
				d.downloadQueue.AutoDetectUpdateJobStatus(oneJob, blockedErr)
				return
			}
			oneJob.Season = season
			oneJob.Episode = episode
			if source == legacyEpisodeSourceFilename {
				oneJob.SceneSeason = season
				oneJob.SceneEpisode = episode
				oneJob.NumberingSource = "explicit filename recovery"
				oneJob.NumberingConfidence = 1
			}
		}
		resolvedRoot := decode.GetSeriesDirRootFPath(oneJob.VideoFPath)
		if resolvedRoot == "" {
			blockedErr := fmt.Errorf("series metadata root not found")
			d.log.WithFields(map[string]interface{}{
				"event": "series_identity_blocked", "reason": "series_root_missing", "job_id": oneJob.Id,
			}).Warn(blockedErr)
			d.downloadQueue.AutoDetectUpdateJobStatus(oneJob, blockedErr)
			return
		}
		oneJob.SeriesRootDirPath = resolvedRoot
		if oldRoot != resolvedRoot || oldSeason != oneJob.Season || oldEpisode != oneJob.Episode {
			bok, err = d.downloadQueue.Update(oneJob)
			if err != nil || !bok {
				d.log.WithError(err).Error("series root repair could not be persisted")
				return
			}
			d.log.WithFields(map[string]interface{}{
				"event": "series_root_repaired", "job_id": oneJob.Id,
			}).Info("series root repaired from nearest tvshow metadata")
		}
	}
	// --------------------------------------------------
	// 判断是否看过，这个只有 Emby 情况下才会生效
	{
		isPlayed := false
		if d.embyHelper != nil {
			// 在拿出来后，如果是有内部媒体服务器媒体 ID 的，那么就去查询是否已经观看过了
			isPlayed, err = d.embyHelper.IsVideoPlayed(settings.Get().EmbySettings, oneJob.MediaServerInsideVideoID)
			if err != nil {
				d.log.Errorln("d.embyHelper.IsVideoPlayed()", oneJob.VideoFPath, err)
				return
			}
		}
		// TODO 暂时屏蔽掉 http api 提交的已看字幕的接口上传
		// 不管如何，只要是发现数据库中有 HTTP API 提交的信息，就认为是看过
		//var videoPlayedInfos []models.ThirdPartSetVideoPlayedInfo
		//dao.GetDb().Where("physical_video_file_full_path = ?", oneJob.VideoFPath).Find(&videoPlayedInfos)
		//if len(videoPlayedInfos) > 0 {
		//	isPlayed = true
		//}
		// --------------------------------------------------
		// 如果已经播放过 且 这个任务的优先级 > 3 ，不是很急的那种，说明是可以设置忽略继续下载的
		if isPlayed == true && oneJob.TaskPriority > task_queue.HighTaskPriorityLevel {
			// 播放过了，那么就标记 ignore
			markJobIgnored(&oneJob)
			bok, err = d.downloadQueue.Update(oneJob)
			if err != nil {
				d.log.Errorln("d.downloadQueue.Update()", err)
				return
			}
			if bok == false {
				d.log.Errorln("d.downloadQueue.Update() Failed")
				return
			}
			d.log.Infoln("Is Played, Ignore This Job")
			return
		}
	}
	// --------------------------------------------------
	// 判断是否需要跳过，因为如果是 Normal 扫描出来的，那么可能因为视频时间久远，下载一次即可
	{
		if oneJob.TaskPriority > task_queue.HighTaskPriorityLevel {
			// 优先级大于 3，那么就不是很急的任务，才需要判断
			if oneJob.VideoType == common2.Movie {
				if nowSubSupplierHub.MovieNeedDlSub(d.fileDownloader.MediaInfoDealers, oneJob.VideoFPath, false) == false {
					// 需要标记忽略
					markJobIgnored(&oneJob)
					bok, err = d.downloadQueue.Update(oneJob)
					if err != nil {
						d.log.Errorln("d.downloadQueue.Update()", err)
						return
					}
					if bok == false {
						d.log.Errorln("d.downloadQueue.Update() Failed")
						return
					}
					d.log.Infoln("MovieNeedDlSub == false, Ignore This Job")
					return
				}
			} else {

				bNeedDlSub, _, err := nowSubSupplierHub.SeriesNeedDlSub(
					d.fileDownloader.MediaInfoDealers,
					oneJob.SeriesRootDirPath,
					false, false)
				if err != nil {
					d.log.Errorln("SeriesNeedDlSub", err)
					return
				}
				// This call intentionally disables subtitle analysis. Its episode map
				// can therefore omit a concrete queued path because of directory scan
				// ordering, filename parsing, or metadata gaps; absence from that map is
				// not evidence that the exact job should be ignored. Only an explicit
				// series-level skip decision may stop the job before it is claimed.
				if shouldIgnoreSeriesBeforeClaim(bNeedDlSub) {
					// 需要标记忽略
					markJobIgnored(&oneJob)
					bok, err = d.downloadQueue.Update(oneJob)
					if err != nil {
						d.log.Errorln("d.downloadQueue.Update()", err)
						return
					}
					if bok == false {
						d.log.Errorln("d.downloadQueue.Update() Failed")
						return
					}
					d.log.Infoln("SeriesNeedDlSub == false, Ignore This Job")
					return
				}
			}
		}
	}
	seriesBatch := []taskQueue2.OneJob{oneJob}
	if oneJob.VideoType != common2.Movie {
		seriesBatch = d.readySeriesBatch(oneJob)
	}
	seriesBatch, err = d.downloadQueue.ClaimBatch(seriesBatch, time.Now())
	if err != nil {
		if err != task_queue.ErrClaimUnavailable {
			d.log.Errorln("d.downloadQueue.ClaimBatch()", err)
		}
		return
	}
	oneJob = seriesBatch[0]
	if len(seriesBatch) > 1 {
		d.log.WithFields(map[string]interface{}{
			"event": "series_batch_claimed", "batch_size": len(seriesBatch), "season": oneJob.Season,
		}).Info("ready series episodes coalesced")
	}
	unregisterSeries := d.registerSeriesWorker(oneJob.SeriesRootDirPath)
	defer unregisterSeries()
	didWork = true
	d.queueClaimLock.Unlock()
	claimLockHeld = false
	endQueueLog := d.startQueueLog(oneJob.Id)
	defer endQueueLog()

	downloadCounter := atomic.AddInt64(&d.queueDownloadCounter, 1)
	jobCtx, cancelJob := context.WithTimeout(d.ctx,
		time.Duration(settings.Get().AdvancedSettings.TaskQueue.OneJobTimeOut)*time.Second)
	defer cancelJob()
	// A single terminal result channel prevents a recovered panic from racing
	// with a separately closed "done" channel and being silently lost.
	done := make(chan queueWorkerResult, 1)

	go func() {
		result := queueWorkerResult{}
		defer func() {
			if p := recover(); p != nil {
				result.panicValue = p
				result.stack = debug.Stack()
			}
			done <- result
		}()
		unlockSeries := d.lockSeriesWorker(oneJob.SeriesRootDirPath)
		defer unlockSeries()

		if oneJob.VideoType == common2.Movie {
			// 电影
			// 具体的下载逻辑 func()
			result.err = d.movieDlFunc(jobCtx, oneJob, downloadCounter)
		} else if oneJob.VideoType == common2.Series || oneJob.VideoType == common2.Anime {
			// 连续剧
			// 具体的下载逻辑 func()
			result.err = d.seriesDlFuncBatch(jobCtx, oneJob, seriesBatch, downloadCounter)
		} else {
			d.log.Errorln("oneJob.VideoType not support, oneJob.VideoType = ", oneJob.VideoType)
		}
	}()
	releaseClaimForRetry := func(delay time.Duration, reason string) {
		if releaseErr := d.downloadQueue.ReleaseClaimsForRetry(seriesBatch, delay); releaseErr != nil {
			d.log.WithError(releaseErr).WithField("reason", reason).Error("release queue claim for retry")
		}
	}
	handleResult := func(result queueWorkerResult) {
		// This is also a final guard for suppliers returning without a terminal
		// outcome and for an unknown/corrupt VideoType. A completed outcome has
		// already released its generation, making this a cheap no-op.
		defer func() {
			delay := time.Minute
			if d.ctx.Err() != nil {
				delay = 15 * time.Second
			}
			releaseClaimForRetry(delay, "worker_exit")
		}()
		if d.ctx.Err() != nil {
			if result.panicValue != nil {
				d.log.Errorf("download worker stopped during shutdown after panic: %v\n%s", result.panicValue, result.stack)
			}
			return
		}
		if result.panicValue != nil {
			panicErr := fmt.Errorf("download worker panic: %v", result.panicValue)
			d.log.Errorf("%v\n%s", panicErr, result.stack)
			outcomes := make([]task_queue.JobOutcome, 0, len(seriesBatch))
			for _, batchJob := range seriesBatch {
				outcomes = append(outcomes, task_queue.JobOutcome{Job: batchJob, Err: panicErr})
			}
			if outcomeErr := d.downloadQueue.ApplyOutcomesReliable(outcomes); outcomeErr != nil && outcomeErr != task_queue.ErrClaimUnavailable {
				d.log.Errorln("persist panic batch outcomes", outcomeErr)
			}
			return
		}
		if result.err != nil {
			d.log.Errorln(result.err)
		}
	}

	result, canceled := waitQueueWorkerResult(d.ctx, done, func() {
		// Administrative shutdown is not a supplier failure. Release the active
		// generation without incrementing attempts/retries or degrading priority;
		// any late worker outcome carries the old token and is ignored.
		releaseClaimForRetry(15*time.Second, "daemon_shutdown")
	})
	handleResult(result)
	if canceled {
		d.log.Warningln("cancel Downloader.QueueDownloader()")
		return
	}
}
