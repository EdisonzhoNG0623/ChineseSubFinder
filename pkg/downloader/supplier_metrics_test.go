package downloader

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestSupplierMetricsCohortUsesQueueMediaType(t *testing.T) {
	tests := []struct {
		videoType common.VideoType
		want      subtitle_metrics.MediaCohort
	}{
		{common.Movie, subtitle_metrics.CohortMovie},
		{common.Series, subtitle_metrics.CohortSeries},
		{common.Anime, subtitle_metrics.CohortAnime},
		{common.VideoType(99), subtitle_metrics.CohortUnknown},
	}
	for _, test := range tests {
		if got := supplierMetricsCohort(test.videoType); got != test.want {
			t.Fatalf("supplierMetricsCohort(%d) = %q, want %q", test.videoType, got, test.want)
		}
	}
}
