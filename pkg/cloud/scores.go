package cloud

import (
	"context"
	"fmt"
	"net/http"
)

// contestScoresPath is the public endpoint on a cloud instance that
// stores a site's schedule and scorecard data for one contest. The
// contest ID in the URL is authoritative; the body is a bare array of
// ContestPlacement rows.
func contestScoresPath(contestID uint) string {
	return fmt.Sprintf("/ui/mod/game/cloud/contests/%d/scores", contestID)
}

// ContestScorecardValue is a single scorecard element value for a
// placement in a contest data push.
type ContestScorecardValue struct {
	Element string `json:"element"`
	Value   int    `json:"value"`
}

// ContestPlacement is a portable, DB-ID-free representation of a
// single match placement with its score and scorecard values. It is
// the unit of the contest data push: everything the cloud stores for
// a site's schedule and scorecards, keyed by the contest ID. State is
// a plain uint because the wire type cannot reference the game
// module's MatchState enum.
type ContestPlacement struct {
	Round      int                     `json:"round"`
	Match      int                     `json:"match"`
	PhaseID    uint                    `json:"phase_id"`
	TeamID     uint                    `json:"team_id"`
	TeamName   string                  `json:"team_name"`
	FieldID    uint                    `json:"field_id"`
	PositionID uint                    `json:"position_id"`
	State      uint                    `json:"state"`
	Score      int                     `json:"score"`
	Values     []ContestScorecardValue `json:"values"`
}

// PushContestData sends a site's schedule and scorecard data for one
// contest to the cloud instance, which replaces the stored data for
// that contest.
func (c *Client) PushContestData(ctx context.Context, contestID uint, placements []ContestPlacement) error {
	return c.DoJSON(ctx, http.MethodPost, contestScoresPath(contestID), nil, placements, nil)
}
