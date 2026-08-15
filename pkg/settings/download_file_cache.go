package settings

import "time"

type DownloadFileCache struct {
	TTL  int    `json:"ttl" default:"336"`   // 默认保留 14 天，避免字幕压缩包无限增长
	Unit string `json:"unit" default:"hour"` // second, hour, 目前仅仅支持 秒和小时
}

func NewDownloadFileCache() *DownloadFileCache {
	return &DownloadFileCache{TTL: 336, Unit: "hour"}
}

func (d *DownloadFileCache) Check() {
	if d == nil {
		return
	}
	if d.Unit == "second" {
		if d.TTL < 86400 || d.TTL > 2592000 {
			d.TTL = 1209600
		}
		return
	}
	if d.Unit != "hour" {
		d.Unit = "hour"
	}
	// 4320 是历史默认值（180 天）。主动迁移到 14 天，最长允许 30 天。
	if d.TTL < 24 || d.TTL > 720 {
		d.TTL = 336
	}
}

func (d *DownloadFileCache) Duration() time.Duration {
	if d == nil {
		return 14 * 24 * time.Hour
	}
	if d.Unit == "second" {
		return time.Duration(d.TTL) * time.Second
	}
	return time.Duration(d.TTL) * time.Hour
}
