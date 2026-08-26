package task_queue

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestGetOneJobExcludingSeries(t *testing.T) {
	const queueName = "testQueueExcludedSeries"
	cache_center.DelDb(queueName)
	defer cache_center.DelDb(queueName)

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	defer queue.Close()

	older := taskQueueTypes.NewOneJob(common.Series, "/media/series-a/S01E01.mkv", DefaultTaskPriorityLevel)
	older.SeriesRootDirPath = "/media/series-a"
	older.AddedTime = emby.Time(time.Now().Add(-time.Hour))
	newer := taskQueueTypes.NewOneJob(common.Series, "/media/series-b/S01E01.mkv", DefaultTaskPriorityLevel)
	newer.SeriesRootDirPath = "/media/series-b"
	newer.AddedTime = emby.Time(time.Now())

	if ok, err := queue.Add(*older); err != nil || !ok {
		t.Fatalf("add older job: ok=%v err=%v", ok, err)
	}
	if ok, err := queue.Add(*newer); err != nil || !ok {
		t.Fatalf("add newer job: ok=%v err=%v", ok, err)
	}

	found, selected, err := queue.GetOneJobExcludingSeries(map[string]struct{}{older.SeriesRootDirPath: {}})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a non-excluded job")
	}
	if selected.Id != newer.Id {
		t.Fatalf("selected excluded series job: got=%s want=%s", selected.SeriesRootDirPath, newer.SeriesRootDirPath)
	}
}

func TestUpdateMovesJobBetweenSeriesIndexes(t *testing.T) {
	const queueName = "testQueueSeriesRootRepair"
	cache_center.DelDb(queueName)
	defer cache_center.DelDb(queueName)

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	defer queue.Close()
	job := taskQueueTypes.NewOneJob(common.Series, "/media/anime/show/S01E01.mkv", DefaultTaskPriorityLevel)
	job.SeriesRootDirPath = "/media/anime"
	if ok, err := queue.Add(*job); err != nil || !ok {
		t.Fatalf("add job: ok=%v err=%v", ok, err)
	}

	job.SeriesRootDirPath = "/media/anime/show"
	if ok, err := queue.Update(*job); err != nil || !ok {
		t.Fatalf("update job: ok=%v err=%v", ok, err)
	}
	if got := queue.GetSeriesJobs("/media/anime"); len(got) != 0 {
		t.Fatalf("old series index retained %d job(s)", len(got))
	}
	if got := queue.GetSeriesJobs("/media/anime/show"); len(got) != 1 || got[0].Id != job.Id {
		t.Fatalf("new series index = %+v", got)
	}
}
