package game

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"

	"github.com/expr-lang/expr"
	"gorm.io/gorm"
)

func (m *Module) DebugGenerateScores(phaseID uint) error {
	ctx := context.Background()

	// Check that the phase information is valid and clear all
	// scorecards.
	_, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).Find(ctx)
	if err != nil {
		return err
	}

	valuators := make(map[string]func() int)
	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(ctx)
	if err != nil {
		return err
	}

	// Produce a set of functions that can populate random values
	// for each game element or state.  These elements don't
	// follow validation logic, so this can generate nonsensical
	// scorecards, the goal is just to have the scorecards exist.
	for _, element := range elements {
		for _, state := range element.States {
			var v func() int
			switch strings.ToLower(element.Type) {
			case "count":
				v = func() int {
					return rand.Intn(state.Max + 1)
				}
			case "boolean":
				v = func() int {
					return rand.Intn(2)
				}
			case "radio":
				vals := []int{}
				for _, val := range state.Values {
					vals = append(vals, int(val.ID))
				}
				v = func() int {
					return vals[rand.Intn(len(vals))]
				}
			}
			valuators[element.EID+"_"+state.SID] = v
		}
	}

	scoreElements, err := gorm.G[ScorecardElement](m.db.DB).Find(ctx)
	if err != nil {
		slog.Error("Error fetching scorecard elements", "error", err)
		return err
	}
	exprFragments := make([]string, len(scoreElements))
	for i := range scoreElements {
		exprFragments[i] = scoreElements[i].Expr
	}

	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phaseID}).
		Find(ctx)
	if err != nil {
		return err
	}

	vMap := make(map[string]int)
	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[MatchScore](tx).Where(&MatchScore{GamePhaseID: phaseID}).Delete(ctx)
		if err != nil {
			return err
		}

		for _, placement := range placements {
			_, err := gorm.G[ScorecardValue](tx).
				Where(&ScorecardValue{MatchPlacementID: placement.ID}).
				Delete(ctx)
			if err != nil {
				return err
			}

			for scElement, valuator := range valuators {
				vMap[scElement] = valuator()
				scv := ScorecardValue{
					MatchPlacementID: placement.ID,
					Element:          scElement,
					Value:            vMap[scElement],
				}
				if err := gorm.G[ScorecardValue](tx).Create(ctx, &scv); err != nil {
					return err
				}
			}

			score, err := expr.Eval(strings.Join(exprFragments, " + "), vMap)
			if err != nil {
				slog.Error("Error evaluating scorecard", "error", err)
				return err
			}
			err = gorm.G[MatchScore](tx).Create(ctx, &MatchScore{
				ID:               placement.ID,
				MatchPlacementID: placement.ID,
				GamePhaseID:      placement.PhaseID,
				TeamID:           placement.TeamID,
				Score:            score.(int),
			})
			if err != nil {
				slog.Error("Error saving match score", "error", err)
				return err
			}
		}

		return nil
	})

	return nil
}
