package game

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
)

// ContestScorecardValue is a single scorecard element value for a
// placement in a contest export.
type ContestScorecardValue struct {
	Element string `json:"element"`
	Value   int    `json:"value"`
}

// ContestPlacement is a portable, DB-ID-free representation of a
// single match placement with its score and scorecard values. It is
// the unit of the contest data export: everything the cloud needs to
// store a site's schedule and scorecards, keyed by the contest ID.
type ContestPlacement struct {
	Round      int                     `json:"round"`
	Match      int                     `json:"match"`
	PhaseID    uint                    `json:"phase_id"`
	TeamID     uint                    `json:"team_id"`
	TeamName   string                  `json:"team_name"`
	FieldID    uint                    `json:"field_id"`
	PositionID uint                    `json:"position_id"`
	State      MatchState              `json:"state"`
	Score      int                     `json:"score"`
	Values     []ContestScorecardValue `json:"values"`
}

// ExportContestData returns the schedule and scorecard data for a
// single contest as portable rows, ordered by round, match, field,
// and position. Placements without a score or scorecard values carry
// zero values and empty slices. An unknown contest ID yields an
// empty slice, not an error.
func (m *Module) ExportContestData(ctx context.Context, contestID uint) ([]ContestPlacement, error) {
	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Team", nil).
		Where(&MatchPlacement{ContestID: contestID}).
		Order("round ASC, match ASC, field_id ASC, position_id ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error loading contest placements", "error", err)
		return nil, err
	}

	scores, err := gorm.G[MatchScore](m.db.DB).
		Where(&MatchScore{ContestID: contestID}).
		Find(ctx)
	if err != nil {
		slog.Error("Error loading contest scores", "error", err)
		return nil, err
	}
	scoreByPlacement := make(map[uint]int, len(scores))
	for _, s := range scores {
		scoreByPlacement[s.MatchPlacementID] = s.Score
	}

	placementIDs := make([]uint, 0, len(placements))
	for _, p := range placements {
		placementIDs = append(placementIDs, p.ID)
	}

	var values []ScorecardValue
	if len(placementIDs) > 0 {
		values, err = gorm.G[ScorecardValue](m.db.DB).
			Where("match_placement_id IN ?", placementIDs).
			Order("element ASC").
			Find(ctx)
		if err != nil {
			slog.Error("Error loading contest scorecard values", "error", err)
			return nil, err
		}
	}
	valuesByPlacement := make(map[uint][]ContestScorecardValue)
	for _, v := range values {
		valuesByPlacement[v.MatchPlacementID] = append(valuesByPlacement[v.MatchPlacementID], ContestScorecardValue{
			Element: v.Element,
			Value:   v.Value,
		})
	}

	out := make([]ContestPlacement, 0, len(placements))
	for _, p := range placements {
		out = append(out, ContestPlacement{
			Round:      p.Round,
			Match:      p.Match,
			PhaseID:    p.PhaseID,
			TeamID:     p.TeamID,
			TeamName:   p.Team.Name,
			FieldID:    p.FieldID,
			PositionID: p.PositionID,
			State:      p.State,
			Score:      scoreByPlacement[p.ID],
			Values:     valuesByPlacement[p.ID],
		})
	}
	return out, nil
}
