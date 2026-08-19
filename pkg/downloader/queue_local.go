package downloader

import (
	"fmt"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"runtime/debug"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	common2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

type queueWorkerPanic struct {
	value interface{}
	stack []byte
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
			if oneJob.VideoType == common2.Series {
				videoType = 1
			}
			if d.ScanLogic.Get(videoType, oneJob.VideoFPath) == true {
				// 需要标记忽略
				oneJob.JobStatus = taskQueue2.Ignore
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
	// 这个任务如果是 series 那么需要考虑是否原始存入的信息是缺失的，需要补全
	{
		if oneJob.VideoType == common2.Series && (oneJob.SeriesRootDirPath == "" || oneJob.Season <= 0 || oneJob.Episode <= 0) {
			// 连续剧的时候需要额外提交信息
			epsVideoNfoInfo, err := decode.GetVideoNfoInfo4OneSeriesEpisode(oneJob.VideoFPath)
			if err != nil {
				d.log.Errorln("decode.GetVideoNfoInfo4OneSeriesEpisode()", err)
				d.log.Infoln("maybe you moved video file to another place or delete it, so will delete this job")
				bok, err = d.downloadQueue.Del(oneJob.Id)
				if err != nil {
					d.log.Errorln("d.downloadQueue.Del()", err)
					return
				}
				if bok == false {
					d.log.Errorln(fmt.Sprintf("d.downloadQueue.Del(%s) == false", oneJob.Id))
					return
				}
				return
			}
			seriesInfoDirPath := decode.GetSeriesDirRootFPath(oneJob.VideoFPath)
			if seriesInfoDirPath == "" {
				d.log.Errorln(fmt.Sprintf("decode.GetSeriesDirRootFPath == Empty, %s", oneJob.VideoFPath))
				d.log.Infoln("you need check the directory structure of a series, so will delete this job")
				bok, err = d.downloadQueue.Del(oneJob.Id)
				if err != nil {
					d.log.Errorln("d.downloadQueue.Del()", err)
					return
				}
				if bok == false {
					d.log.Errorln(fmt.Sprintf("d.downloadQueue.Del(%s) == false", oneJob.Id))
					return
				}
				return
			}
			oneJob.Season = epsVideoNfoInfo.Season
			oneJob.Episode = epsVideoNfoInfo.Episode
			oneJob.SeriesRootDirPath = seriesInfoDirPath
		}
	}
	// --------------------------------------------------
	// 这个视频文件不存在了
	{
		isBlue, _, _ := decode.IsFakeBDMVWorked(oneJob.VideoFPath)
		if isBlue == false && pkg.IsFile(oneJob.VideoFPath) == false {
			// 不是蓝光，那么就判断文件是否存在，不存在，那么就标记 ignore
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
			oneJob.JobStatus = taskQueue2.Ignore
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
					oneJob.JobStatus = taskQueue2.Ignore
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
			} else if oneJob.VideoType == common2.Series {

				bNeedDlSub, seriesInfo, err := nowSubSupplierHub.SeriesNeedDlSub(
					d.fileDownloader.MediaInfoDealers,
					oneJob.SeriesRootDirPath,
					false, false)
				if err != nil {
					d.log.Errorln("SeriesNeedDlSub", err)
					return
				}
				needMarkSkip := false
				if bNeedDlSub == false {
					// 需要跳过
					needMarkSkip = true
				} else {
					// 需要下载的 Eps 是否与 Normal 判断这个连续剧中有那些剧集需要下载的，情况符合。通过下载的时间来判断
					epsKey := pkg.GetEpisodeKeyName(oneJob.Season, oneJob.Episode)
					_, found := seriesInfo.NeedDlEpsKeyList[epsKey]
					if found == false {
						// 需要跳过
						needMarkSkip = true
					}
				}

				if needMarkSkip == true {
					// 需要标记忽略
					oneJob.JobStatus = taskQueue2.Ignore
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
	// 取出来后，需要标记为正在下载
	oneJob.JobStatus = taskQueue2.Downloading
	oneJob.ForceRun = false
	bok, err = d.downloadQueue.Update(oneJob)
	if err != nil {
		d.log.Errorln("d.downloadQueue.Update()", err)
		return
	}
	if bok == false {
		d.log.Errorln("d.downloadQueue.Update() Failed")
		return
	}
	unregisterSeries := d.registerSeriesWorker(oneJob.SeriesRootDirPath)
	defer unregisterSeries()
	didWork = true
	d.queueClaimLock.Unlock()
	claimLockHeld = false
	endQueueLog := d.startQueueLog(oneJob.Id)
	defer endQueueLog()

	downloadCounter := d.queueDownloadCounter.Add(1)
	// 创建一个 chan 用于任务的中断和超时
	done := make(chan interface{}, 1)
	// 接收内部任务的 panic
	panicChan := make(chan queueWorkerPanic, 1)

	go func() {
		defer func() {
			if p := recover(); p != nil {
				panicChan <- queueWorkerPanic{value: p, stack: debug.Stack()}
			}
			close(done)
			close(panicChan)
		}()
		unlockSeries := d.lockSeriesWorker(oneJob.SeriesRootDirPath)
		defer unlockSeries()

		if oneJob.VideoType == common2.Movie {
			// 电影
			// 具体的下载逻辑 func()
			done <- d.movieDlFunc(d.ctx, oneJob, downloadCounter)
		} else if oneJob.VideoType == common2.Series {
			// 连续剧
			// 具体的下载逻辑 func()
			done <- d.seriesDlFunc(d.ctx, oneJob, downloadCounter)
		} else {
			d.log.Errorln("oneJob.VideoType not support, oneJob.VideoType = ", oneJob.VideoType)
			done <- nil
		}
	}()

	select {
	case err := <-done:
		// 跳出 select，可以外层继续，不会阻塞在这里
		if err != nil {
			d.log.Errorln(err)
		}
		// 刷新视频的缓存结构
		//d.UpdateInfo(oneJob)

		break
	case p, ok := <-panicChan:
		// panicChan 正常关闭时也可被 select 选中，此时收到的是 nil。
		// 只有真正捕获到 panic 值时才向外抛出，避免 panic(nil) 刷屏。
		if ok && p.value != nil {
			panicErr := fmt.Errorf("download worker panic: %v", p.value)
			d.log.Errorf("%v\n%s", panicErr, p.stack)
			d.downloadQueue.AutoDetectUpdateJobStatus(oneJob, panicErr)
		}
	case <-d.ctx.Done():
		{
			// 取消这个 context
			d.log.Warningln("cancel Downloader.QueueDownloader()")
			return
		}
	}
}
