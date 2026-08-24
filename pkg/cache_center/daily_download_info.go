package cache_center

import (
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center/models"
)

type DailySupplierCount struct {
	SupplierName string `json:"supplier_name"`
	Count        int    `json:"count"`
}

// DailyDownloadCountSummary aggregates all public-IP buckets without exposing
// any recorded IP address to the diagnostics API.
func (c *CacheCenter) DailyDownloadCountSummary(whichDay ...string) ([]DailySupplierCount, error) {
	defer c.locker.Unlock()
	c.locker.Lock()
	day := time.Now().Format("2006-01-02")
	if len(whichDay) > 0 && whichDay[0] != "" {
		day = whichDay[0]
	}
	out := make([]DailySupplierCount, 0)
	result := c.db.Model(&models.DailyDownloadInfo{}).
		Select("supplier_name, SUM(count) AS count").
		Where("which_day = ?", day).
		Group("supplier_name").
		Scan(&out)
	return out, result.Error
}

// DailyDownloadCountGet 根据字幕提供者的名称，获取今日下载计数的次数，仅仅统计次数，并不确认是那个视频的字幕下载
// whichDay nowTime.Format("2006-01-02")
func (c *CacheCenter) DailyDownloadCountGet(supplierName string, publicIP string, whichDay ...string) (int, error) {
	defer c.locker.Unlock()
	c.locker.Lock()

	dailyDownloadInfos := c.dailyDownloadCountGet(supplierName, publicIP, whichDay...)
	if len(dailyDownloadInfos) == 0 {
		return 0, nil
	}

	return dailyDownloadInfos[0].Count, nil
}

func (c *CacheCenter) dailyDownloadCountGet(supplierName string, publicIP string, whichDay ...string) []models.DailyDownloadInfo {

	var dailyDownloadInfos []models.DailyDownloadInfo
	whichDayStr := ""
	if len(whichDay) > 0 {
		whichDayStr = whichDay[0]
	} else {
		nowTime := time.Now()
		whichDayStr = nowTime.Format("2006-01-02")
	}
	c.db.Where("supplier_name = ? AND public_ip = ? AND  which_day = ?", supplierName, publicIP, whichDayStr).Find(&dailyDownloadInfos)

	return dailyDownloadInfos
}

// DailyDownloadCountAdd 根据字幕提供者的名称，今日下载计数的次数+1，仅仅统计次数，并不确认是哪个视频的字幕下载
func (c *CacheCenter) DailyDownloadCountAdd(supplierName string, publicIP string, whichDay ...string) (int, error) {
	defer c.locker.Unlock()
	c.locker.Lock()

	dailyDownloadCounts := c.dailyDownloadCountGet(supplierName, publicIP, whichDay...)

	whichDayStr := ""
	if len(whichDay) > 0 {
		whichDayStr = whichDay[0]
	} else {
		nowTime := time.Now()
		whichDayStr = nowTime.Format("2006-01-02")
	}

	outCount := 0
	if len(dailyDownloadCounts) == 0 {
		dailyDownloadInfo := models.DailyDownloadInfo{
			SupplierName: supplierName,
			PublicIP:     publicIP,
			WhichDay:     whichDayStr,
			Count:        1,
		}
		outCount = 1
		c.db.Create(&dailyDownloadInfo)
	} else {
		dailyDownloadCounts[0].Count += 1
		outCount = dailyDownloadCounts[0].Count
		c.db.Save(&dailyDownloadCounts[0])
	}
	return outCount, nil
}
