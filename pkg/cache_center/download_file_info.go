package cache_center

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center/models"
)

func (c *CacheCenter) DownloadFileAdd(subInfo *supplier.SubInfo) error {
	defer c.locker.Unlock()
	c.locker.Lock()

	if subInfo.FileUrl == "" {
		return errors.New("subInfo FileUrl is empty")
	}

	// 只支持秒或者小时为单位
	tmpTTL := time.Duration(settings.Get().AdvancedSettings.DownloadFileCache.TTL) * time.Second
	if settings.Get().AdvancedSettings.DownloadFileCache.Unit == "hour" {
		tmpTTL = time.Duration(settings.Get().AdvancedSettings.DownloadFileCache.TTL) * time.Hour
	} else {
		tmpTTL = time.Duration(settings.Get().AdvancedSettings.DownloadFileCache.TTL) * time.Second
	}

	b, err := json.Marshal(subInfo)
	if err != nil {
		return err
	}

	// 保存到本地文件
	todayString := time.Now().Format("2006-01-02")
	saveFPath := filepath.Join(c.downloadFileSaveRootPath, todayString, subInfo.GetUID())
	err = pkg.WriteFile(saveFPath, b)
	if err != nil {
		return err
	}
	relPath, err := filepath.Rel(c.downloadFileSaveRootPath, saveFPath)
	if err != nil {
		return err
	}

	df := models.DownloadFileInfo{
		UID:            subInfo.GetUID(),
		RelPath:        relPath,
		ExpirationTime: time.Now().Add(tmpTTL),
	}

	c.db.Save(&df)

	return nil
}

func (c *CacheCenter) DownloadFileGet(fileUrlUID string) (bool, *supplier.SubInfo, error) {
	defer c.locker.Unlock()
	c.locker.Lock()

	var dfs []models.DownloadFileInfo
	c.db.Where("uid = ?", fileUrlUID).Find(&dfs)

	if len(dfs) == 0 {
		return false, nil, nil
	}
	if !dfs[0].ExpirationTime.After(time.Now()) {
		localFileFPath := filepath.Join(c.downloadFileSaveRootPath, dfs[0].RelPath)
		if err := removeCacheFileWithinRoot(c.downloadFileSaveRootPath, localFileFPath); err != nil {
			return false, nil, err
		}
		if err := c.db.Unscoped().Delete(&models.DownloadFileInfo{}, "uid = ?", fileUrlUID).Error; err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}

	localFileFPath := filepath.Join(c.downloadFileSaveRootPath, dfs[0].RelPath)
	if pkg.IsFile(localFileFPath) == false {
		return false, nil, nil
	}

	bytes, err := os.ReadFile(localFileFPath)
	if err != nil {
		return false, nil, err
	}

	var subInfo supplier.SubInfo
	err = json.Unmarshal(bytes, &subInfo)
	if err != nil {
		return false, nil, err
	}

	return true, &subInfo, nil
}

type DownloadCacheCleanupReport struct {
	ExpiredRecords int64
	StaleFolders   int
}

// CleanupDownloadFileCache removes expired records and caps on-disk retention.
// Date folders older than maxAge are deleted even when legacy records carry the
// historical 180-day TTL.
func (c *CacheCenter) CleanupDownloadFileCache(now time.Time, maxAge time.Duration) (DownloadCacheCleanupReport, error) {
	defer c.locker.Unlock()
	c.locker.Lock()

	report := DownloadCacheCleanupReport{}
	var expired []models.DownloadFileInfo
	if err := c.db.Where("expiration_time <= ?", now).Find(&expired).Error; err != nil {
		return report, err
	}
	for _, record := range expired {
		cachePath := filepath.Join(c.downloadFileSaveRootPath, record.RelPath)
		if err := removeCacheFileWithinRoot(c.downloadFileSaveRootPath, cachePath); err != nil {
			return report, err
		}
	}
	if len(expired) > 0 {
		result := c.db.Unscoped().Where("expiration_time <= ?", now).Delete(&models.DownloadFileInfo{})
		if result.Error != nil {
			return report, result.Error
		}
		report.ExpiredRecords = result.RowsAffected
	}

	entries, err := os.ReadDir(c.downloadFileSaveRootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, err
	}
	cutoff := now.Add(-maxAge)
	cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folderDate, parseErr := time.ParseInLocation("2006-01-02", entry.Name(), now.Location())
		if parseErr != nil || !folderDate.Before(cutoffDay) {
			continue
		}
		folderPath := filepath.Join(c.downloadFileSaveRootPath, entry.Name())
		if err = os.RemoveAll(folderPath); err != nil {
			return report, err
		}
		prefix := entry.Name() + string(os.PathSeparator) + "%"
		if err = c.db.Unscoped().Where("rel_path LIKE ?", prefix).Delete(&models.DownloadFileInfo{}).Error; err != nil {
			return report, err
		}
		report.StaleFolders++
	}
	if report.ExpiredRecords > 0 || report.StaleFolders > 0 {
		c.Log.Infof("Download cache cleanup removed %d expired records and %d stale date folders", report.ExpiredRecords, report.StaleFolders)
	}
	return report, nil
}

func removeCacheFileWithinRoot(root, path string) error {
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove cache path outside root: %s", path)
	}
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent != root {
		_ = os.Remove(parent)
	}
	return nil
}
