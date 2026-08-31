package backend

import (
	"fmt"
	"net/http"

	"github.com/arl/statsviz"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/tmdb_api"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/controllers/base"
	v1 "github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/controllers/v1"
	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/cron_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/pre_job"
	"github.com/gin-gonic/gin"
)

func InitRouter(
	router *gin.Engine,
	cronHelper *cron_helper.CronHelper,
	restartSignal chan interface{},
	preJob *pre_job.PreJob,
) (*base.ControllerBase, *v1.ControllerBase) {

	// ----------------------------------------------
	// 设置 TMDB API 的本地 Client，用户自己的 API Key
	var err error
	var tmdbApi *tmdb_api.TmdbApi
	if settings.Get().AdvancedSettings.TmdbApiSettings.Enable == true &&
		settings.Get().AdvancedSettings.TmdbApiSettings.ApiKey != "" {

		tmdbApi, err = tmdb_api.NewTmdbHelper(cronHelper.Logger, settings.Get().AdvancedSettings.TmdbApiSettings.ApiKey, settings.Get().AdvancedSettings.TmdbApiSettings.UseAlternateBaseURL)
		if err != nil {
			cronHelper.Logger.Panicln("NewTmdbHelper", err)
		}
		if tmdbApi.Alive() == false {
			// 如果 tmdbApi 不可用，那么就不使用
			cronHelper.Logger.Errorln("tmdbApi.Alive() == false")
			tmdbApi = nil
		}
	}
	cronHelper.FileDownloader.MediaInfoDealers.SetTmdbHelperInstance(tmdbApi)
	// ----------------------------------------------
	cbBase := base.NewControllerBase(cronHelper.FileDownloader, restartSignal, preJob)
	cbV1 := v1.NewControllerBase(cronHelper, restartSignal)
	// --------------------------------------------------
	// 静态文件服务器
	// 添加电影的
	for i, path := range settings.Get().CommonSettings.MoviePaths {

		nowUrl := "/movie_dir_" + fmt.Sprintf("%d", i)
		cbV1.SetPathUrlMapItem(path, nowUrl)
		registerProtectedStaticFS(router, nowUrl, http.Dir(path))
	}
	// 添加连续剧的
	for i, path := range settings.Get().CommonSettings.SeriesPaths {

		nowUrl := "/series_dir_" + fmt.Sprintf("%d", i)
		cbV1.SetPathUrlMapItem(path, nowUrl)
		registerProtectedStaticFS(router, nowUrl, http.Dir(path))
	}
	// --------------------------------------------------
	// 性能监视
	if settings.Get().AdvancedSettings.DebugMode == true {
		// 如果是 DebugMode 那么开启性能监控
		router.GET("/debug/statsviz/*filepath", middle.CheckResourceAuth(), func(context *gin.Context) {
			if context.Param("filepath") == "/ws" {
				statsviz.Ws(context.Writer, context.Request)
				return
			}
			statsviz.IndexAtRoot("/debug/statsviz").ServeHTTP(context.Writer, context.Request)
		})
	}
	// --------------------------------------------------
	// 基础的路由
	router.GET("/system-status", cbBase.SystemStatusHandler)

	router.POST("/login", cbBase.LoginHandler)
	router.POST("/logout", middle.CheckAuth(), middle.ClearResourceAuthCookie(), cbBase.LogoutHandler)

	router.POST("/change-pwd", middle.CheckAuth(), cbBase.ChangePwdHandler)

	setupAware := router.Group("")
	setupAware.Use(middle.CheckAuthAfterSetup())
	{
		setupAware.POST("/pre-job", cbBase.PreJobHandler)
		setupAware.POST("/setup", cbBase.SetupHandler)
		setupAware.POST("/check-path", cbBase.CheckPathHandler)
		setupAware.POST("/check-emby-path", cbBase.CheckEmbyPathHandler)
		setupAware.POST("/check-proxy", cbBase.CheckProxyHandler)
		setupAware.POST("/check-cron", cbBase.CheckCronHandler)
		setupAware.GET("/def-settings", cbBase.DefSettingsHandler)
		setupAware.POST("/check-emby-settings", cbBase.CheckEmbySettingsHandler)
		setupAware.POST("/check-tmdb-api-settings", cbBase.CheckTmdbApiHandler)
	}

	// v1路由: /v1/xxx
	GroupV1 := router.Group("/" + cbV1.GetVersion())
	{
		GroupV1.Use(middle.CheckAuth(), middle.IssueResourceAuthCookie())

		GroupV1.GET("/settings", cbV1.SettingsHandler)
		GroupV1.PUT("/settings", cbV1.SettingsHandler)

		GroupV1.POST("/daemon/start", cbV1.DaemonStartHandler)
		GroupV1.POST("/daemon/stop", cbV1.DaemonStopHandler)
		GroupV1.GET("/daemon/status", cbV1.DaemonStatusHandler)

		GroupV1.GET("/jobs/list", cbV1.JobsListHandler)
		GroupV1.GET("/jobs", cbV1.JobsPageHandler)
		GroupV1.GET("/suppliers", cbV1.SupplierDiagnosticsHandler)
		GroupV1.POST("/suppliers/check", cbV1.SupplierCheckHandler)
		GroupV1.GET("/overview", cbV1.OverviewHandler)
		GroupV1.GET("/ai/status", cbV1.AIStatusHandler)
		GroupV1.POST("/ai/test", cbV1.AITestHandler)
		GroupV1.POST("/jobs/change-job-status", cbV1.ChangeJobStatusHandler)
		GroupV1.POST("/jobs/log", cbV1.JobLogHandler)

		//GroupV1.POST("/video/list/refresh", cbV1.RefreshVideoListHandler)
		GroupV1.GET("/video/list/refresh-status", cbV1.RefreshVideoListStatusHandler)
		//GroupV1.GET("/video/list", cbV1.VideoListHandler)
		GroupV1.POST("/video/list/add", cbV1.VideoListAddHandler)

		GroupV1.POST("/video/list/refresh_main_list", cbV1.RefreshMainList)
		GroupV1.GET("/video/list/video_main_list", cbV1.VideoMainList)
		GroupV1.POST("/video/list/movie_poster", cbV1.MoviePoster)
		GroupV1.POST("/video/list/series_poster", cbV1.SeriesPoster)
		GroupV1.POST("/video/list/one_movie_subs", cbV1.OneMovieSubs)
		GroupV1.POST("/video/list/one_series_subs", cbV1.OneSeriesSubs)
		GroupV1.POST("/video/list/scan_skip_info", cbV1.ScanSkipInfo)
		GroupV1.PUT("/video/list/scan_skip_info", cbV1.ScanSkipInfo)

		GroupV1.POST("/subtitles/refresh_media_server_sub_list", cbV1.RefreshMediaServerSubList)
		GroupV1.POST("/subtitles/manual_upload_2_local", cbV1.ManualUploadSubtitle2Local)
		GroupV1.POST("/subtitles/manual_upload_result", cbV1.ManualUploadSubtitleResult)
		GroupV1.GET("/subtitles/list_manual_upload_2_local_job", cbV1.ListManualUploadSubtitle2LocalJob)
		GroupV1.POST("/subtitles/is_manual_upload_2_local_in_queue", cbV1.IsManualUploadSubtitle2LocalJobInQueue)
		GroupV1.POST("/subtitles/get_generate_upload_url_info", cbV1.GetGenerateUploadURLHandle)

		GroupV1.POST("/preview/clean_up", cbV1.PreviewCleanUp)
		GroupV1.POST("/preview/search_other_web", cbV1.PreviewSearchOtherWeb)
		GroupV1.POST("/preview/video_f_path_2_imdb_info", cbV1.PreviewVideoFPath2IMDBInfo)
	}

	// Browser-native HLS requests cannot attach the management header in every
	// engine. A short path-bound ticket authenticates the playlist, whose longer
	// path-bound stream ticket can authenticate only segment GETs.
	GroupV1Resources := router.Group("/" + cbV1.GetVersion())
	GroupV1Resources.GET(
		"/preview/playlist/:videofpathbase64",
		middle.CheckHLSPlaylistAuth(),
		cbV1.HlsPlaylist,
	)
	GroupV1Resources.GET(
		"/preview/segments/:resolution/:segment/:videofpathbase64",
		middle.CheckHLSStreamAuth(),
		cbV1.HlsSegment,
	)

	GroupAPIV1 := router.Group("/api/v1")
	{
		GroupAPIV1.Use(middle.CheckApiAuth())

		GroupAPIV1.POST("/add-job", cbV1.AddJobHandler)
		GroupAPIV1.GET("/job-status", cbV1.GetJobStatusHandler)
		GroupAPIV1.POST("/change-job-status", cbV1.ChangeJobStatusHandler)
		GroupAPIV1.POST("/run-scan", cbV1.RunScanHandler)
		GroupAPIV1.POST("/add-video-played-info", cbV1.AddVideoPlayedInfoHandler)
		GroupAPIV1.DELETE("/del-video-played-info", cbV1.DelVideoPlayedInfoHandler)
	}

	return cbBase, cbV1
}

func registerProtectedStaticFS(router *gin.Engine, route string, fileSystem http.FileSystem) {
	group := router.Group(route)
	group.Use(middle.CheckResourceAuth())
	group.StaticFS("", fileSystem)
}
