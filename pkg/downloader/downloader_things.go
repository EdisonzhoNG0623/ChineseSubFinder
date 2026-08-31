package downloader

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/subparser"

	subcommon "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
)

// oneVideoSelectBestSub 一个视频，选择最佳的一个字幕（也可以保存所有网站第一个最佳字幕）
func (d *Downloader) oneVideoSelectBestSub(oneVideoFullPath string, organizeSubFiles []string) error {
	return d.oneVideoSelectBestSubForCohort(oneVideoFullPath, organizeSubFiles, subtitle_metrics.CohortUnknown)
}

func supplierMetricsCohort(videoType common.VideoType) subtitle_metrics.MediaCohort {
	switch videoType {
	case common.Movie:
		return subtitle_metrics.CohortMovie
	case common.Anime:
		return subtitle_metrics.CohortAnime
	case common.Series:
		return subtitle_metrics.CohortSeries
	default:
		return subtitle_metrics.CohortUnknown
	}
}

// oneVideoSelectBestSubForCohort keeps the legacy selection behavior while
// attributing successful selection/write conversion to a bounded media cohort.
func (d *Downloader) oneVideoSelectBestSubForCohort(oneVideoFullPath string, organizeSubFiles []string,
	cohort subtitle_metrics.MediaCohort) error {

	// 如果没有则直接跳过
	if organizeSubFiles == nil || len(organizeSubFiles) < 1 {
		return common.AllSiteDownloadSubNotFound
	}
	if d.SaveSubHelper == nil {
		return errors.New("subtitle save helper is not initialized")
	}
	return d.SaveSubHelper.WithVideoWriteLock(oneVideoFullPath, func(writer *save_sub_helper.VideoWriteTransaction) error {
		return d.oneVideoSelectBestSubForCohortLocked(oneVideoFullPath, organizeSubFiles, cohort, writer)
	})
}

func (d *Downloader) oneVideoSelectBestSubForCohortLocked(oneVideoFullPath string, organizeSubFiles []string,
	cohort subtitle_metrics.MediaCohort, writer *save_sub_helper.VideoWriteTransaction) error {

	// Manual upload persists its skip marker before releasing this video's write
	// transaction. Re-check after lock admission so an already-running automatic
	// search cannot overwrite a manual subtitle that finished while it waited.
	if d.ScanLogic != nil && cohort != subtitle_metrics.CohortUnknown {
		videoType := 1
		if cohort == subtitle_metrics.CohortMovie {
			videoType = 0
		}
		if d.ScanLogic.Get(videoType, oneVideoFullPath) {
			d.log.Infoln("Automatic subtitle save skipped after manual override:", oneVideoFullPath)
			return nil
		}
	}

	var err error
	// 得到目标视频文件的文件名
	videoFileName := filepath.Base(oneVideoFullPath)
	// -------------------------------------------------
	// 调试缓存，把下载好的字幕写到对应的视频目录下，方便调试
	if settings.Get().AdvancedSettings.DebugMode == true {

		err = pkg.CopyFiles2DebugFolder([]string{videoFileName}, organizeSubFiles)
		if err != nil {
			// 这个错误可以忍
			d.log.Errorln("copySubFile2DesFolder", err)
		}
	}
	// -------------------------------------------------
	// Snapshot existing default/forced entries without changing them. They are
	// demoted only after every requested subtitle has been selected, processed,
	// and published successfully. This keeps selection and write failures from
	// changing the previously installed visible state.
	markerSnapshots, snapshotErr := writer.SnapshotSubtitleMarkers()
	if snapshotErr != nil {
		d.log.WithError(snapshotErr).Warnln("snapshot existing subtitle markers", oneVideoFullPath)
	}
	if settings.Get().AdvancedSettings.SaveMultiSub == false {
		// 选择最优的一个字幕
		var finalSubFile *subparser.FileInfo
		finalSubFile = d.mk.SelectOneSubFile(organizeSubFiles)
		if finalSubFile == nil {
			outString := fmt.Sprintln("Found", len(organizeSubFiles), " subtitles but not one fit:", oneVideoFullPath)
			d.log.Warnln(outString)
			return errors.New(outString)
		}
		subtitle_metrics.RecordSelectionForCohort(finalSubFile.FromWhereSite, cohort)
		/*
			这里还有一个梗，Emby、jellyfin 支持 default 和 forced 扩展字段
			但是，plex 只支持 forced
			那么就比较麻烦，干脆，normal 的命名格式化实例，就不设置 default 了，forced 不想用，因为可能会跟你手动选择的字幕冲突（下次观看的时候，理论上也可能不会）
		*/
		// 判断配置文件中的字幕命名格式化的选择
		bSetDefault := true
		if d.subNameFormatter == subcommon.Normal {
			bSetDefault = false
		}
		// 找到了，写入文件
		err = writer.WriteSubFile(*finalSubFile, "", bSetDefault, false)
		if err != nil {
			return errors.New(fmt.Sprintf("SaveMultiSub: %v, writeSubFile2VideoPath, Error: %v ", settings.Get().AdvancedSettings.SaveMultiSub, err))
		}
		subtitle_metrics.RecordSaveForCohort(finalSubFile.FromWhereSite, cohort)
	} else {
		// 每个网站 Top1 的字幕
		siteNames, finalSubFiles := d.mk.SelectEachSiteTop1SubFile(organizeSubFiles)
		if len(siteNames) == 0 || len(finalSubFiles) == 0 {
			outString := fmt.Sprintln("SelectEachSiteTop1SubFile found none sub file")
			d.log.Warnln(outString)
			return errors.New(outString)
		}
		if len(siteNames) != len(finalSubFiles) {
			return fmt.Errorf("SelectEachSiteTop1SubFile returned mismatched results: %d sites, %d subtitles",
				len(siteNames), len(finalSubFiles))
		}
		// 多网站 Top 1 字幕保存的时候，第一个设置为 Default 即可
		/*
			由于新功能支持了字幕命名格式的选择，那么如果触发了多个字幕保存的逻辑，如果不调整
			则会遇到，top1 先写入，然后 top2 覆盖 top1 ，以此类推的情况出现
			所以如果开启了 Normal SubNameFormatter 的功能，则要反序写入文件
			如果是 Emby 的字幕命名格式则无需考虑此问题，因为每个网站只会有一个字幕，且字幕命名格式决定了不会重复写入覆盖
		*/
		if d.subNameFormatter == subcommon.Emby {
			// Publish non-default site results first. The authoritative default is
			// committed last, after every operation that can still fail, so a later
			// site error cannot replace an existing same-path default with a partial
			// multi-site result.
			for i := 1; i < len(finalSubFiles); i++ {
				file := finalSubFiles[i]
				subtitle_metrics.RecordSelectionForCohort(file.FromWhereSite, cohort)
				err = writer.WriteSubFile(file, siteNames[i], false, false)
				if err != nil {
					return errors.New(fmt.Sprintf("SaveMultiSub: %v, writeSubFile2VideoPath, Error: %v ", settings.Get().AdvancedSettings.SaveMultiSub, err))
				}
				subtitle_metrics.RecordSaveForCohort(file.FromWhereSite, cohort)
			}
			defaultFile := finalSubFiles[0]
			subtitle_metrics.RecordSelectionForCohort(defaultFile.FromWhereSite, cohort)
			err = writer.WriteSubFile(defaultFile, siteNames[0], true, false)
			if err != nil {
				return errors.New(fmt.Sprintf("SaveMultiSub: %v, writeSubFile2VideoPath, Error: %v ", settings.Get().AdvancedSettings.SaveMultiSub, err))
			}
			subtitle_metrics.RecordSaveForCohort(defaultFile.FromWhereSite, cohort)
		} else {
			// 默认这里就是 normal 模式
			// 逆序写入
			/*
				这里还有一个梗，Emby、jellyfin 支持 default 和 forced 扩展字段
				但是，plex 只支持 forced
				那么就比较麻烦，干脆，normal 的命名格式化实例，就不设置 default 了，forced 不想用，因为可能会跟你手动选择的字幕冲突（下次观看的时候，理论上也可能不会）
			*/
			for i := len(finalSubFiles) - 1; i > -1; i-- {
				subtitle_metrics.RecordSelectionForCohort(finalSubFiles[i].FromWhereSite, cohort)
				err = writer.WriteSubFile(finalSubFiles[i], siteNames[i], false, false)
				if err != nil {
					return errors.New(fmt.Sprintf("SaveMultiSub: %v, writeSubFile2VideoPath, Error: %v ", settings.Get().AdvancedSettings.SaveMultiSub, err))
				}
				subtitle_metrics.RecordSaveForCohort(finalSubFiles[i].FromWhereSite, cohort)
			}
		}
	}
	writer.DemoteSubtitleMarkers(markerSnapshots)
	// -------------------------------------------------

	return nil
}

// saveFullSeasonSub 这里就需要单独存储到连续剧每一季的文件夹的特殊文件夹中。需要跟 DeleteOneSeasonSubCacheFolder 关联起来
func (d *Downloader) saveFullSeasonSub(seriesInfo *series.SeriesInfo, organizeSubFiles map[string][]string) map[string][]string {
	fullSeasonSubDict := mappedCollectionEpisodes(seriesInfo, organizeSubFiles)
	if len(fullSeasonSubDict) < 2 {
		return map[string][]string{}
	}
	inventory := append(append([]series.EpisodeInfo(nil), seriesInfo.ArchiveEpList...), seriesInfo.EpList...)
	episodesByKey := make(map[string]series.EpisodeInfo, len(inventory))
	for _, episode := range inventory {
		episodesByKey[pkg.GetEpisodeKeyName(episode.Season, episode.Episode)] = episode
	}
	for episodeKey, subs := range fullSeasonSubDict {
		episode := episodesByKey[episodeKey]
		seasonKey := pkg.GetEpisodeKeyName(episode.Season, 0)
		for _, sub := range subs {
			subFileName := filepath.Base(sub)

			newSeasonSubRootPath, err := pkg.GetDebugFolderByName([]string{
				filepath.Base(seriesInfo.DirPath),
				"Sub_" + seasonKey})
			if err != nil {
				d.log.Errorln("saveFullSeasonSub.GetDebugFolderByName", subFileName, err)
				continue
			}

			newSubFullPath := filepath.Join(newSeasonSubRootPath, subFileName)
			err = pkg.CopyFile(sub, newSubFullPath)
			if err != nil {
				d.log.Errorln("saveFullSeasonSub.CopyFile", subFileName, err)
				continue
			}
		}
	}

	return fullSeasonSubDict
}

func mappedCollectionEpisodes(seriesInfo *series.SeriesInfo, organizeSubFiles map[string][]string) map[string][]string {
	out := make(map[string][]string)
	if seriesInfo == nil {
		return out
	}
	allEpisodes := append(append([]series.EpisodeInfo(nil), seriesInfo.ArchiveEpList...), seriesInfo.EpList...)
	inventory := make(map[string]struct{}, len(allEpisodes))
	for _, episode := range allEpisodes {
		if episode.Season > 0 && episode.Episode > 0 {
			inventory[pkg.GetEpisodeKeyName(episode.Season, episode.Episode)] = struct{}{}
		}
	}
	for episodeKey, subtitles := range organizeSubFiles {
		if len(subtitles) == 0 {
			continue
		}
		if _, exists := inventory[episodeKey]; !exists {
			continue
		}
		out[episodeKey] = append([]string(nil), subtitles...)
	}
	return out
}
