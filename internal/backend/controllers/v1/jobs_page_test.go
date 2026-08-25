package v1

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestFilterJobsAndSummary(t *testing.T) {
	now := time.Now()
	jobs := []taskQueueTypes.OneJob{
		{VideoName: "Anime 01.mkv", VideoType: common.Anime, JobStatus: taskQueueTypes.Waiting, TaskPriority: 6, ErrorInfo: "No Sub Found", UpdateTime: emby.Time(now), ForceRun: true, SeriesRootDirPath: "/media/anime", Season: 1},
		{VideoName: "Anime 02.mkv", VideoType: common.Anime, JobStatus: taskQueueTypes.Waiting, TaskPriority: 6, SeriesRootDirPath: "/media/anime", Season: 1},
		{VideoName: "Movie.mkv", VideoType: common.Movie, JobStatus: taskQueueTypes.Done, TaskPriority: 5, UpdateTime: emby.Time(now)},
	}
	status := int(taskQueueTypes.Waiting)
	filtered := filterJobs(jobs, jobPageQuery{Status: &status, ErrorCategory: "NO_SUBTITLE"})
	if len(filtered) != 1 || filtered[0].VideoName != "Anime 01.mkv" {
		t.Fatalf("unexpected filtered jobs: %+v", filtered)
	}
	summary := summarizeJobs(jobs, now)
	if summary.Total != 3 || summary.ByVideoType["anime"] != 2 || summary.ByStatus["done"] != 1 ||
		summary.WaitingSeries != 2 || summary.SeriesGroups != 1 || summary.BatchableGroups != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestSortJobsByName(t *testing.T) {
	jobs := []taskQueueTypes.OneJob{{VideoName: "Zulu"}, {VideoName: "Alpha"}}
	sortJobs(jobs, "name", "asc")
	if jobs[0].VideoName != "Alpha" {
		t.Fatalf("unexpected sort: %+v", jobs)
	}
}

func TestNewJobViewIncludesAnimeFallbackPlan(t *testing.T) {
	job := taskQueueTypes.OneJob{
		VideoType: common.Anime, SeriesName: "Example Anime", SeriesRootDirPath: "/media/Example Anime",
		Season: 8, Episode: 11, AbsoluteEpisode: 288, SceneSeason: 8, SceneEpisode: 10,
		NumberingSource: "anime-lists", NumberingConfidence: 1,
	}
	view := newJobView(job, time.Now())
	if !view.Identity.IsAnime || view.Identity.AbsoluteEpisode != 288 || len(view.Identity.QueryPlan) < 3 {
		t.Fatalf("unexpected identity view: %+v", view.Identity)
	}
	foundAbsolute := false
	foundScene := false
	for _, query := range view.Identity.QueryPlan {
		if query.Kind == "ABSOLUTE" && query.Absolute == 288 {
			foundAbsolute = true
		}
		if query.Kind == "SCENE" && query.Season == 8 && query.Episode == 10 {
			foundScene = true
		}
	}
	if !foundAbsolute || !foundScene {
		t.Fatalf("missing fallback variants: %+v", view.Identity.QueryPlan)
	}
}
