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

	types, err := gorm.G[ScoreType](m.db.DB).Order("`order` ASC").Find(ctx)
	if err != nil {
		return err
	}

	if len(types) == 0 {
		slog.Warn("No score types found, nothing to do")
		return nil
	}

	return m.db.Transaction(func(tx *gorm.DB) error {
		if _, err := gorm.G[TeamScoreValue](tx).
			Where("1 = 1").
			Delete(ctx); err != nil {
			return err
		}

		for _, team := range teams {
			for _, st := range types {
				value := TeamScoreValue{
					TeamID:      team.ID,
					ScoreTypeID: st.ID,
					Value:       rand.Float32() * st.Max,
				}
				if err := gorm.G[TeamScoreValue](tx).Create(ctx, &value); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
