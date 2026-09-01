package best

import (
	"context"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
)

type TeamScore struct {
	Team  team.Team
	Score *ExternalScores
}

func (m *Module) ListTeamScores(ctx context.Context) ([]TeamScore, error) {
	teams, err := gorm.G[team.Team](m.db.DB).
		Preload("Division", nil).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		return nil, err
	}

	scores, err := gorm.G[ExternalScores](m.db.DB).Find(ctx)
	if err != nil {
		return nil, err
	}

	byTeam := make(map[uint]*ExternalScores, len(scores))
	for i := range scores {
		byTeam[scores[i].TeamID] = &scores[i]
	}

	out := make([]TeamScore, 0, len(teams))
	for i := range teams {
		out = append(out, TeamScore{
			Team:  teams[i],
			Score: byTeam[teams[i].ID],
		})
	}

	return out, nil
}

func (m *Module) uiViewScores(w http.ResponseWriter, r *http.Request) {
	teamScores, err := m.ListTeamScores(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	m.ws.DoTemplate(w, r, "views/best/scores.p2", pongo2.Context{"scores": teamScores})
}
