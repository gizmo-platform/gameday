package best

import (
	"context"
	"log/slog"
	"math/rand"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
)

func (m *Module) DebugGenerateScores() error {
	ctx := context.Background()

	teams, err := gorm.G[team.Team](m.db.DB).Find(ctx)
	if err != nil {
		return err
	}

	if len(teams) == 0 {
		slog.Warn("No teams found, nothing to do")
		return nil
	}

	valuators := map[string]func() float32{
		"Notebook":  func() float32 { return float32(rand.Intn(MaxScoreNotebook + 1)) },
		"Marketing": func() float32 { return float32(rand.Intn(MaxScoreMarketing + 1)) },
		"Poster":    func() float32 { return float32(rand.Intn(MaxScorePoster + 1)) },
		"Video":     func() float32 { return float32(rand.Intn(MaxScoreVideo + 1)) },
	}

	return m.db.Transaction(func(tx *gorm.DB) error {
		if _, err := gorm.G[ExternalScores](tx).
			Where("1 = 1").
			Delete(ctx); err != nil {
			return err
		}

		for _, team := range teams {
			scores := ExternalScores{
				TeamID:    team.ID,
				Notebook:  valuators["Notebook"](),
				Marketing: valuators["Marketing"](),
				Poster:    valuators["Poster"](),
				Video:     valuators["Video"](),
			}
			if err := gorm.G[ExternalScores](tx).Create(ctx, &scores); err != nil {
				return err
			}
		}
		return nil
	})
}
