package manual_upload_sub_2_local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_parser/ass"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_parser/srt"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	subCommon "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/emby"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/normal"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_parser_hub"
	"github.com/sirupsen/logrus"
)

type exactSkipCall struct {
	videoPath string
	skip      bool
}

type recordingExactSkipSetter struct {
	calls []exactSkipCall
	err   error
}

func (r *recordingExactSkipSetter) SetExactVideoPathSkip(videoPath string, skip bool) error {
	r.calls = append(r.calls, exactSkipCall{videoPath: videoPath, skip: skip})
	return r.err
}

func TestNewManualUploadSub2Local(t *testing.T) {

	logger := log_helper.GetLogger4Tester()
	saveHelper := save_sub_helper.NewSaveSubHelper(logger, normal.NewFormatter(logger), nil)
	processor := NewManualUploadSub2Local(logger, saveHelper, nil)
	if processor == nil {
		t.Fatal("constructor returned nil")
	}
}

func TestProcessSubPersistsExactSkipAcrossFormatterMediaCrossovers(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	currentSettings := settings.Get()
	currentSettings.AdvancedSettings.FixTimeLine = false
	currentSettings.ExperimentalFunction.AutoChangeSubEncode.Enable = false
	currentSettings.ExperimentalFunction.ChsChtChanger.Enable = false

	tests := []struct {
		name      string
		formatter ifaces.ISubFormatter
		videoName string
	}{
		{
			name:      "movie cohort with normal formatter",
			formatter: normal.NewFormatter(log_helper.GetLogger4Tester()),
			videoName: "Feature (2026).mkv",
		},
		{
			name:      "series or anime cohort with emby formatter",
			formatter: emby.NewFormatter(),
			videoName: "Show.S01E02.mkv",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := log_helper.GetLogger4Tester()
			testDir := t.TempDir()
			videoPath := filepath.Join(testDir, test.videoName)
			subtitlePath := filepath.Join(testDir, "upload.srt")
			if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			const subtitle = "1\n00:00:00,000 --> 00:00:02,000\n这是一条中文字幕。\n"
			if err := os.WriteFile(subtitlePath, []byte(subtitle), 0o600); err != nil {
				t.Fatal(err)
			}

			recorder := &recordingExactSkipSetter{}
			processor := &ManualUploadSub2Local{
				log:              logger,
				saveSubHelper:    save_sub_helper.NewSaveSubHelper(logger, test.formatter, nil),
				scanLogic:        recorder,
				subNameFormatter: subCommon.FormatterName(test.formatter.GetFormatterFormatterName()),
				subParserHub:     sub_parser_hub.NewSubParserHub(logger, ass.NewParser(logger), srt.NewParser(logger)),
			}

			if err := processor.processSub(&Job{VideoFPath: videoPath, SubFPath: subtitlePath}); err != nil {
				t.Fatalf("processSub() error = %v", err)
			}
			if len(recorder.calls) != 1 {
				t.Fatalf("exact skip setter calls = %d, want 1", len(recorder.calls))
			}
			if got := recorder.calls[0]; got.videoPath != videoPath || !got.skip {
				t.Fatalf("exact skip setter call = %+v, want videoPath=%q skip=true", got, videoPath)
			}
		})
	}
}

func TestProcessSubDemotesOnlyExactVideoMarkers(t *testing.T) {
	configureManualUploadTestSettings(t)
	logger := log_helper.GetLogger4Tester()
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "Movie.mkv")
	siblingVideoPath := filepath.Join(mediaDir, "Movie Extended.mkv")
	targetMarker := filepath.Join(mediaDir, "Movie.zh.default.srt")
	siblingMarker := filepath.Join(mediaDir, "Movie Extended.zh.default.srt")
	subtitlePath := filepath.Join(mediaDir, "upload.srt")
	fixtures := map[string][]byte{
		videoPath:        nil,
		siblingVideoPath: nil,
		targetMarker:     []byte("old target default"),
		siblingMarker:    []byte("sibling default must remain"),
		subtitlePath:     manualSubtitleFixture("新的手工中文字幕"),
	}
	for path, contents := range fixtures {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	recorder := &recordingExactSkipSetter{}
	processor := newManualUploadProcessorForTest(logger, normal.NewFormatter(logger), recorder)
	if err := processor.processSub(&Job{VideoFPath: videoPath, SubFPath: subtitlePath}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(targetMarker); !os.IsNotExist(err) {
		t.Fatalf("superseded target marker still exists or stat failed: %v", err)
	}
	if got, err := os.ReadFile(siblingMarker); err != nil || string(got) != "sibling default must remain" {
		t.Fatalf("prefix sibling marker changed: contents=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(mediaDir, "Movie.zh.srt")); err != nil || string(got) == "old target default" {
		t.Fatalf("manual subtitle was not published at the target path: contents=%q err=%v", got, err)
	}
}

func TestProcessSubWriteFailurePreservesExistingMarkerAndDoesNotSkip(t *testing.T) {
	configureManualUploadTestSettings(t)
	logger := log_helper.GetLogger4Tester()
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "Movie.mkv")
	markedPath := filepath.Join(mediaDir, "Movie.zh.default.srt")
	blockedDestination := filepath.Join(mediaDir, "Movie.zh.srt")
	subtitlePath := filepath.Join(mediaDir, "upload.srt")
	for path, contents := range map[string][]byte{
		videoPath:    nil,
		markedPath:   []byte("existing default must survive"),
		subtitlePath: manualSubtitleFixture("无法写入的手工字幕"),
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(blockedDestination, 0o700); err != nil {
		t.Fatal(err)
	}

	recorder := &recordingExactSkipSetter{}
	processor := newManualUploadProcessorForTest(logger, normal.NewFormatter(logger), recorder)
	if err := processor.processSub(&Job{VideoFPath: videoPath, SubFPath: subtitlePath}); err == nil {
		t.Fatal("processSub() succeeded despite a directory blocking the subtitle destination")
	}
	if got, err := os.ReadFile(markedPath); err != nil || string(got) != "existing default must survive" {
		t.Fatalf("write failure changed the prior marker: contents=%q err=%v", got, err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("failed manual upload persisted skip calls: %+v", recorder.calls)
	}
}

func TestProcessSubReportsExactSkipPersistenceFailure(t *testing.T) {
	configureManualUploadTestSettings(t)
	logger := log_helper.GetLogger4Tester()
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "Movie.mkv")
	subtitlePath := filepath.Join(mediaDir, "upload.srt")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subtitlePath, manualSubtitleFixture("数据库失败场景"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := &recordingExactSkipSetter{err: errors.New("database is read-only")}
	processor := newManualUploadProcessorForTest(logger, normal.NewFormatter(logger), recorder)
	err := processor.processSub(&Job{VideoFPath: videoPath, SubFPath: subtitlePath})
	if err == nil || !strings.Contains(err.Error(), "persist exact-path manual subtitle override") {
		t.Fatalf("processSub() error = %v, want persistence failure", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("exact skip setter calls = %d, want 1", len(recorder.calls))
	}
}

func configureManualUploadTestSettings(t *testing.T) {
	t.Helper()
	settings.SetConfigRootPath(t.TempDir())
	currentSettings := settings.Get()
	currentSettings.AdvancedSettings.FixTimeLine = false
	currentSettings.ExperimentalFunction.AutoChangeSubEncode.Enable = false
	currentSettings.ExperimentalFunction.ChsChtChanger.Enable = false
}

func newManualUploadProcessorForTest(log *logrus.Logger, formatter ifaces.ISubFormatter,
	skipSetter exactVideoPathSkipSetter) *ManualUploadSub2Local {
	return &ManualUploadSub2Local{
		log:              log,
		saveSubHelper:    save_sub_helper.NewSaveSubHelper(log, formatter, nil),
		scanLogic:        skipSetter,
		subNameFormatter: subCommon.FormatterName(formatter.GetFormatterFormatterName()),
		subParserHub:     sub_parser_hub.NewSubParserHub(log, ass.NewParser(log), srt.NewParser(log)),
	}
}

func manualSubtitleFixture(text string) []byte {
	return []byte("1\n00:00:00,000 --> 00:00:02,000\n" + text + "\n")
}
