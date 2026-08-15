package cache_center

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center/models"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
)

func TestMain(m *testing.M) {
	configRoot, err := os.MkdirTemp("", "csf-cache-center-test-")
	if err != nil {
		panic(err)
	}
	settings.SetConfigRootPath(configRoot)
	code := m.Run()
	_ = os.RemoveAll(configRoot)
	os.Exit(code)
}

func TestCacheCenter_DownloadFileAdd(t *testing.T) {
	cc := NewCacheCenter("testFile", log_helper.GetLogger4Tester())

	subInfo := supplier.NewSubInfo(
		"test",
		1,
		"name",
		language.ChineseSimple,
		"url123123",
		0,
		0,
		"ext",
		[]byte{1, 2, 3, 4, 5},
	)
	err := cc.DownloadFileAdd(subInfo)
	if err != nil {
		t.Fatal(err)
	}

	bok, getSubInfo, err := cc.DownloadFileGet(subInfo.GetUID())
	if err != nil {
		t.Fatal(err)
	}
	if bok == false {
		t.Fatal("bok == false")
	}

	if subInfo.FileUrl != getSubInfo.FileUrl {
		t.Fatal("subInfo.FileUrl != getSubInfo.FileUrl")
	}
}

func TestCleanupDownloadFileCache(t *testing.T) {
	const cacheName = "cleanup_download_file_cache_test"
	DelDb(cacheName)
	cc := NewCacheCenter(cacheName, log_helper.GetLogger4Tester())
	t.Cleanup(func() {
		cc.Close()
		DelDb(cacheName)
	})

	now := time.Now()
	oldDate := now.AddDate(0, 0, -30).Format("2006-01-02")
	newDate := now.Format("2006-01-02")
	oldRelPath := filepath.Join(oldDate, "old")
	newRelPath := filepath.Join(newDate, "new")
	expiredRelPath := filepath.Join(newDate, "expired")
	for _, relPath := range []string{oldRelPath, newRelPath, expiredRelPath} {
		fullPath := filepath.Join(cc.downloadFileSaveRootPath, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	records := []models.DownloadFileInfo{
		{UID: "old", RelPath: oldRelPath, ExpirationTime: now.Add(90 * 24 * time.Hour)},
		{UID: "new", RelPath: newRelPath, ExpirationTime: now.Add(90 * 24 * time.Hour)},
		{UID: "expired", RelPath: expiredRelPath, ExpirationTime: now.Add(-time.Hour)},
	}
	if err := cc.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}

	report, err := cc.CleanupDownloadFileCache(now, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExpiredRecords != 1 || report.StaleFolders != 1 {
		t.Fatalf("unexpected cleanup report: %+v", report)
	}
	if _, err = os.Stat(filepath.Join(cc.downloadFileSaveRootPath, oldRelPath)); !os.IsNotExist(err) {
		t.Fatal("stale cache file was not removed")
	}
	if _, err = os.Stat(filepath.Join(cc.downloadFileSaveRootPath, expiredRelPath)); !os.IsNotExist(err) {
		t.Fatal("expired cache file was not removed")
	}
	if _, err = os.Stat(filepath.Join(cc.downloadFileSaveRootPath, newRelPath)); err != nil {
		t.Fatalf("recent cache file was removed: %v", err)
	}
	var oldRecordCount int64
	if err = cc.db.Unscoped().Model(&models.DownloadFileInfo{}).Where("uid = ?", "old").Count(&oldRecordCount).Error; err != nil {
		t.Fatal(err)
	}
	if oldRecordCount != 0 {
		t.Fatal("stale cache database record was not removed")
	}
}
