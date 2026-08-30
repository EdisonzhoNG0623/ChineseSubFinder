package video_scan_and_refresh_helper

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

// mergeSeriesSearchAliases adds the remote canonical titles to the local
// scanner evidence while preserving the existing supplier fallback order.
func mergeSeriesSearchAliases(existing []string, mediaInfo *models.MediaInfo) []string {
	if mediaInfo == nil {
		return queueTypes.NormalizeSearchAliases(existing...)
	}
	values := make([]string, 0, len(existing)+3)
	values = append(values, existing...)
	values = append(values, mediaInfo.TitleCn, mediaInfo.TitleEn, mediaInfo.OriginalTitle)
	return queueTypes.NormalizeSearchAliases(values...)
}
