package best

import "time"

type ScoreType struct {
	ID    uint
	Key   string `gorm:"uniqueIndex"`
	Name  string
	Max   float32
	Order int
}

func (ScoreType) TableName() string {
	return "best_score_types"
}

type TeamScoreValue struct {
	ID          uint
	TeamID      uint `gorm:"uniqueIndex:idx_best_team_score,composite"`
	ScoreTypeID uint `gorm:"uniqueIndex:idx_best_team_score,composite"`
	Value       float32
	SetAt       time.Time
}

func (TeamScoreValue) TableName() string {
	return "best_score_values"
}
