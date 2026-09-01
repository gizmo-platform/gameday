package best

import (
	"github.com/gizmo-platform/gameday/modules/team"
)

const (
	MaxScoreNotebook  = 300
	MaxScoreMarketing = 250
	MaxScorePoster    = 100
	MaxScoreVideo     = 100
)

type ExternalScores struct {
	ID uint

	Team   team.Team
	TeamID uint

	Notebook  float32
	Marketing float32
	Poster    float32
	Video     float32
}
