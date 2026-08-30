package downloader

import (
	"fmt"
	"path/filepath"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
)

const (
	legacyEpisodeSourceNFO      = "nfo"
	legacyEpisodeSourceFilename = "filename"
)

func resolveLegacySeriesEpisode(videoPath string) (season, episode int, source string, err error) {
	episodeInfo, nfoErr := decode.GetVideoNfoInfo4OneSeriesEpisode(videoPath)
	if nfoErr == nil && episodeInfo.Season > 0 && episodeInfo.Episode > 0 {
		return episodeInfo.Season, episodeInfo.Episode, legacyEpisodeSourceNFO, nil
	}

	found, filenameSeason, filenameEpisode, filenameErr := decode.GetSeasonAndEpisodeFromEpisodeFileName(filepath.Base(videoPath))
	if filenameErr != nil {
		return 0, 0, "", fmt.Errorf("parse episode filename: %w", filenameErr)
	}
	if found {
		return filenameSeason, filenameEpisode, legacyEpisodeSourceFilename, nil
	}
	if nfoErr != nil {
		return 0, 0, "", nfoErr
	}
	return 0, 0, "", fmt.Errorf("episode metadata has no usable season and episode")
}
