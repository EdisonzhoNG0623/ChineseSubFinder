package models

import "gorm.io/gorm"

type TaskOutcomeDaily struct {
	gorm.Model
	WhichDay  string `gorm:"column:which_day;uniqueIndex:idx_task_outcome_day_type_outcome;not null"`
	VideoType string `gorm:"column:video_type;uniqueIndex:idx_task_outcome_day_type_outcome;not null"`
	Outcome   string `gorm:"column:outcome;uniqueIndex:idx_task_outcome_day_type_outcome;not null"`
	Count     int    `gorm:"column:count;not null;default:0"`
}
