package cache_center

import (
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center/models"
	"gorm.io/gorm/clause"
)

type TaskOutcomeCount struct {
	WhichDay  string `json:"which_day"`
	VideoType string `json:"video_type"`
	Outcome   string `json:"outcome"`
	Count     int    `json:"count"`
}

func (c *CacheCenter) TaskOutcomeAdd(whichDay, videoType, outcome string) error {
	defer c.locker.Unlock()
	c.locker.Lock()
	if whichDay == "" {
		whichDay = time.Now().Format("2006-01-02")
	}
	record := models.TaskOutcomeDaily{WhichDay: whichDay, VideoType: videoType, Outcome: outcome, Count: 1}
	return c.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "which_day"}, {Name: "video_type"}, {Name: "outcome"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"count": gormExpr("count + 1")}),
	}).Create(&record).Error
}

// gormExpr is isolated to keep the upsert expression constant and never mix
// external input into SQL fragments.
func gormExpr(expression string) clause.Expr { return clause.Expr{SQL: expression} }

func (c *CacheCenter) TaskOutcomeSummary(days int, now time.Time) ([]TaskOutcomeCount, error) {
	defer c.locker.Unlock()
	c.locker.Lock()
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	start := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	out := make([]TaskOutcomeCount, 0)
	result := c.db.Model(&models.TaskOutcomeDaily{}).
		Select("which_day, video_type, outcome, count").
		Where("which_day >= ?", start).
		Order("which_day ASC, video_type ASC, outcome ASC").
		Scan(&out)
	return out, result.Error
}
