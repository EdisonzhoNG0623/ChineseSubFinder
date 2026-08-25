package downloader

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/series_helper"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	"golang.org/x/net/context"
)

func (d *Downloader) movieDlFunc(ctx context.Context, job taskQueue2.OneJob, downloadIndex int64) error {

	nowSubSupplierHub := d.getSubSupplierHub()
	if nowSubSupplierHub == nil {
		d.log.Infoln("Wait SupplierCheck Update *subSupplierHub, movieDlFunc Skip this time")
		return nil
	}
	if nowSubSupplierHub.Suppliers == nil || len(nowSubSupplierHub.Suppliers) < 1 {
		d.log.Infoln("Wait SupplierCheck Update *subSupplierHub, movieDlFunc Skip this time")
		return nil
	}

	// 字幕都下载缓存好了，需要抉择存哪一个，优先选择中文双语的，然后到中文
	organizeSubFiles, err := nowSubSupplierHub.DownloadSub4MovieContext(ctx, job.VideoFPath, downloadIndex)
	if err != nil {
		err = errors.New(fmt.Sprintf("subSupplierHub.DownloadSub4Movie: %v, %v", job.VideoFPath, err))
		d.downloadQueue.AutoDetectUpdateJobStatus(job, err)
		return err
	}
	// 返回的两个值都是 nil 的时候，就是没有下载到字幕
	if organizeSubFiles == nil || len(organizeSubFiles) < 1 {
		d.log.Infoln(task_queue.ErrNoSubFound.Error(), filepath.Base(job.VideoFPath))
		d.downloadQueue.AutoDetectUpdateJobStatus(job, task_queue.ErrNoSubFound)
		return nil
	}
	if err = ctx.Err(); err != nil {
		d.downloadQueue.AutoDetectUpdateJobStatus(job, err)
		return err
	}

	err = d.oneVideoSelectBestSub(job.VideoFPath, organizeSubFiles)
	if err != nil {
		d.downloadQueue.AutoDetectUpdateJobStatus(job, err)
		return err
	}

	d.downloadQueue.AutoDetectUpdateJobStatus(job, nil)

	// TODO 刷新字幕，这里是 Emby 的，如果是其他的，需要再对接对应的媒体服务器
	if settings.Get().EmbySettings.Enable == true && d.embyHelper != nil && job.MediaServerInsideVideoID != "" {
		if err = ctx.Err(); err != nil {
			return err
		}

		d.log.Infoln("字幕下载完毕，尝试刷新 Emby 中对应字幕", job.VideoFPath, job.MediaServerInsideVideoID)
		err = d.embyHelper.EmbyApi.UpdateVideoSubList(settings.Get().EmbySettings, job.MediaServerInsideVideoID)
		if err != nil {
			d.log.Errorln("UpdateVideoSubList", job.VideoFPath, job.MediaServerInsideVideoID, "Error:", err)
			return err
		}
	} else {
		if settings.Get().EmbySettings.Enable == false {
			d.log.Infoln("字幕下载完毕，尝试刷新 Emby 中对应字幕", job.VideoFPath, "Skip, because Emby enable is false")
		} else if d.embyHelper == nil {
			d.log.Infoln("字幕下载完毕，尝试刷新 Emby 中对应字幕", job.VideoFPath, "Skip, because EmbyHelper is nil")
		} else if job.MediaServerInsideVideoID == "" {
			d.log.Infoln("字幕下载完毕，尝试刷新 Emby 中对应字幕", job.VideoFPath, "Skip, because MediaServerInsideVideoID is empty")
		}
	}

	return nil
}

func (d *Downloader) seriesDlFunc(ctx context.Context, job taskQueue2.OneJob, downloadIndex int64) error {
	return d.seriesDlFuncBatch(ctx, job, []taskQueue2.OneJob{job}, downloadIndex)
}

func (d *Downloader) seriesDlFuncBatch(ctx context.Context, job taskQueue2.OneJob, batchJobs []taskQueue2.OneJob, downloadIndex int64) error {

	nowSubSupplierHub := d.getSubSupplierHub()
	if nowSubSupplierHub == nil || nowSubSupplierHub.Suppliers == nil || len(nowSubSupplierHub.Suppliers) < 1 {
		d.log.Infoln("Wait SupplierCheck Update *subSupplierHub, seriesDlFunc Skip this time")
		return nil
	}
	if len(batchJobs) == 0 {
		batchJobs = []taskQueue2.OneJob{job}
	}
	var err error
	epsMap := buildSeriesEpisodeMap(batchJobs)
	// 这里拿到了这一部连续剧的所有的剧集信息，以及所有下载到的字幕信息
	seriesInfo, err := series_helper.ReadSeriesInfoFromDir(
		d.fileDownloader.MediaInfoDealers, job.SeriesRootDirPath,
		settings.Get().AdvancedSettings.TaskQueue.ExpirationTime,
		false,
		false,
		epsMap)
	if err != nil {
		err = errors.New(fmt.Sprintf("seriesDlFunc.ReadSeriesInfoFromDir, Error: %v", err))
		d.completeSeriesBatch(batchJobs, nil, nil, err)
		return err
	}
	primaryEpisodeKey := pkg.GetEpisodeKeyName(job.Season, job.Episode)
	if _, stillNeeded := seriesInfo.NeedDlEpsKeyList[primaryEpisodeKey]; !stillNeeded {
		job.JobStatus = taskQueue2.Ignore
		job.ErrorInfo = ""
		job.ForceRun = false
		updated, updateErr := d.downloadQueue.Update(job)
		if updateErr != nil {
			return fmt.Errorf("seriesDlFunc.IgnoreSatisfiedPrimary: %w", updateErr)
		}
		if !updated {
			return errors.New("seriesDlFunc.IgnoreSatisfiedPrimary: queue job not found")
		}
		return nil
	}
	activeBatch := make([]taskQueue2.OneJob, 0, len(batchJobs))
	for _, batchJob := range batchJobs {
		key := pkg.GetEpisodeKeyName(batchJob.Season, batchJob.Episode)
		if _, stillNeeded := seriesInfo.NeedDlEpsKeyList[key]; stillNeeded {
			activeBatch = append(activeBatch, batchJob)
			continue
		}
		batchJob.JobStatus = taskQueue2.Ignore
		batchJob.ErrorInfo = ""
		batchJob.ForceRun = false
		updated, updateErr := d.downloadQueue.Update(batchJob)
		if updateErr != nil {
			err = fmt.Errorf("seriesDlFunc.IgnoreSatisfiedCompanion: %w", updateErr)
			d.completeSeriesBatch(activeBatch, nil, nil, err)
			return err
		}
		if !updated {
			err = errors.New("seriesDlFunc.IgnoreSatisfiedCompanion: queue job not found")
			d.completeSeriesBatch(activeBatch, nil, nil, err)
			return err
		}
	}
	batchJobs = activeBatch
	seriesInfo.PrimaryEpisodeKey = primaryEpisodeKey
	// 下载好的字幕文件
	var organizeSubFiles map[string][]string
	// 下载的接口是统一的
	organizeSubFiles, err = nowSubSupplierHub.DownloadSub4SeriesContext(ctx, job.SeriesRootDirPath,
		seriesInfo,
		downloadIndex)
	// DownloadSub4Series enriches alternate anime numbering before suppliers
	// run. Persist that identity with each batched queue outcome.
	identities := make(map[string]seriesIdentity, len(seriesInfo.NeedDlEpsKeyList))
	for key, episode := range seriesInfo.NeedDlEpsKeyList {
		identities[key] = seriesIdentity{
			absoluteEpisode:     episode.AbsoluteEpisode,
			sceneSeason:         episode.SceneSeason,
			sceneEpisode:        episode.SceneEpisode,
			numberingSource:     episode.NumberingSource,
			numberingConfidence: episode.NumberingConfidence,
			seriesName:          seriesInfo.Name,
		}
	}
	batchJobs = enrichSeriesBatchJobs(batchJobs, identities)
	if err != nil {
		err = errors.New(fmt.Sprintf("seriesDlFunc.DownloadSub4Series %v S%vE%v %v", filepath.Base(job.SeriesRootDirPath), job.Season, job.Episode, err))
		d.completeSeriesBatch(batchJobs, nil, nil, err)
		return err
	}
	// 是否下载到字幕了
	if organizeSubFiles == nil || len(organizeSubFiles) < 1 {
		d.log.Infoln(task_queue.ErrNoSubFound.Error(), filepath.Base(job.VideoFPath), job.Season, job.Episode)
		d.completeSeriesBatch(batchJobs, nil, nil, task_queue.ErrNoSubFound)
		return nil
	}

	savedEpisodes := make(map[string]struct{})
	saveErrors := make(map[string]error)
	// 只针对需要下载字幕的视频进行字幕的选择保存
	for epsKey, episodeInfo := range seriesInfo.NeedDlEpsKeyList {
		saveErr := runSubtitleSaveWithContext(ctx, func() error {
			return d.oneVideoSelectBestSub(episodeInfo.FileFullPath, organizeSubFiles[epsKey])
		})
		if errors.Is(saveErr, context.Canceled) || errors.Is(saveErr, context.DeadlineExceeded) {
			err = fmt.Errorf("cancel at NeedDlEpsKeyList.oneVideoSelectBestSub, %v S%dE%d: %w",
				seriesInfo.Name, episodeInfo.Season, episodeInfo.Episode, saveErr)
			d.completeSeriesBatch(batchJobs, savedEpisodes, saveErrors, err)
			return err
		}
		if saveErr != nil {
			saveErrors[epsKey] = saveErr
			d.log.Errorln(saveErr)
		} else {
			savedEpisodes[epsKey] = struct{}{}
		}
	}
	// A multi-episode archive is already persisted in FileDownloader's shared
	// cache. Fan its extracted episode files out now so queued episodes do not
	// fetch and unpack the same ASSRT collection one by one.
	backfillReport, backfillErr := d.backfillSeriesCollection(ctx, job, organizeSubFiles)
	if backfillErr != nil {
		// Backfill is additive. The explicitly requested episode still decides the
		// current job outcome, while partial fan-out remains safely reusable.
		d.log.Warningln("seriesDlFunc.backfillSeriesCollection", backfillErr)
	}
	if backfillReport.Saved > 0 {
		d.log.Infof("seriesDlFunc collection cache backfilled %d episodes and completed %d queued jobs",
			backfillReport.Saved, backfillReport.QueueMarked)
	}
	for episodeKey := range backfillReport.SatisfiedKeys {
		savedEpisodes[episodeKey] = struct{}{}
	}
	// 这里会拿到一份季度字幕的列表比如，Key 是 S1E0 S2E0 S3E0，value 是新的存储位置
	fullSeasonSubDict := d.saveFullSeasonSub(seriesInfo, organizeSubFiles)
	// TODO 季度的字幕包，应该优先于零散的字幕吧，暂定就这样了，注意是全部都替换
	// 需要与有下载需求的季交叉
	for _, episodeInfo := range seriesInfo.EpList {

		_, ok := seriesInfo.NeedDlSeasonDict[episodeInfo.Season]
		if ok == false {
			continue
		}

		seasonEpsKey := pkg.GetEpisodeKeyName(episodeInfo.Season, episodeInfo.Episode)
		fullSeasonSubs, found := fullSeasonEpisodeSubs(fullSeasonSubDict, seasonEpsKey)
		if !found {
			d.log.Infoln("seriesDlFunc.saveFullSeasonSub, no sub found, Skip", seasonEpsKey)
			continue
		}

		saveErr := runSubtitleSaveWithContext(ctx, func() error {
			return d.oneVideoSelectBestSub(episodeInfo.FileFullPath, fullSeasonSubs)
		})
		if errors.Is(saveErr, context.Canceled) || errors.Is(saveErr, context.DeadlineExceeded) {
			err = fmt.Errorf("cancel at NeedDlEpsKeyList.oneVideoSelectBestSub, %v S%dE%d: %w",
				seriesInfo.Name, episodeInfo.Season, episodeInfo.Episode, saveErr)
			d.completeSeriesBatch(batchJobs, savedEpisodes, saveErrors, err)
			return err
		}
		if saveErr != nil {
			saveErrors[seasonEpsKey] = saveErr
			d.log.Errorln(saveErr)
		} else {
			savedEpisodes[seasonEpsKey] = struct{}{}
		}
	}
	// 是否清理全季的缓存字幕文件夹
	if settings.Get().AdvancedSettings.SaveFullSeasonTmpSubtitles == false {
		err = sub_helper.DeleteOneSeasonSubCacheFolder(seriesInfo.DirPath)
		if err != nil {
			d.log.Errorln("seriesDlFunc.DeleteOneSeasonSubCacheFolder", err)
		}
	}

	primaryErr := d.completeSeriesBatch(batchJobs, savedEpisodes, saveErrors, task_queue.ErrNoSubFound)
	if primaryErr != nil {
		return primaryErr
	}
	// TODO 刷新字幕，这里是 Emby 的，如果是其他的，需要再对接对应的媒体服务器
	if settings.Get().EmbySettings.Enable == true && d.embyHelper != nil {

		if job.MediaServerInsideVideoID != "" {
			d.log.Infoln("字幕下载完毕，尝试刷新 Emby 中对应字幕", job.SeriesRootDirPath, job.MediaServerInsideVideoID, job.Season, job.Episode)
			err = d.embyHelper.EmbyApi.UpdateVideoSubList(settings.Get().EmbySettings, job.MediaServerInsideVideoID)
			if err != nil {
				d.log.Errorln("UpdateVideoSubList", job.SeriesRootDirPath, job.MediaServerInsideVideoID, job.Season, job.Episode, "Error:", err)
				return err
			}
		} else {
			d.log.Warningln("字幕下载完毕，尝试刷新 Emby 中对应字幕，跳过，因为 MediaServerInsideVideoID 为空", job.SeriesRootDirPath, job.Season, job.Episode)
		}
	}

	return nil
}

func fullSeasonEpisodeSubs(fullSeasonSubDict map[string][]string, episodeKey string) ([]string, bool) {
	subs := fullSeasonSubDict[episodeKey]
	return subs, len(subs) > 0
}
