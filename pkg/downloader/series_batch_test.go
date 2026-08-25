package downloader

import (
	"reflect"
	"testing"

	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestBuildSeriesEpisodeMapDeduplicatesAndSorts(t *testing.T) {
	got := buildSeriesEpisodeMap([]taskQueueTypes.OneJob{
		{Season: 1, Episode: 4}, {Season: 1, Episode: 2}, {Season: 1, Episode: 4}, {Season: 2, Episode: 1},
	})
	want := map[int][]int{1: {2, 4}, 2: {1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("episode map = %#v, want %#v", got, want)
	}
}
