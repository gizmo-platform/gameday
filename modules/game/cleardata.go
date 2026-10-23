package game

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
)

// errNoContest is returned when a contest-scoped operation is asked
// about contest 0, which in this codebase means "no contest selected"
// rather than a real contest. Clearing it would delete rows that
// belong to nothing, so it is refused.
var errNoContest = errors.New("no contest selected")

// contestCountRow is a grouped count of contest-keyed rows.
type contestCountRow struct {
	ContestID uint
	Cnt       int64
}

// ContestDataCounts implements modules.ContestDataClearer: it reports,
// for every contest that has stored push data, how many placements and
// scorecard values the cloud holds for it. Both queries are grouped
// over the whole table so a list of many contests costs a fixed two
// queries.
//
// A database where the cloud placement tables have not been created
// yet contributes no rows: the list page must keep working regardless
// of migration state.
func (m *Module) ContestDataCounts(ctx context.Context) (map[uint][]modules.ContestDataRow, error) {
	out := make(map[uint][]modules.ContestDataRow)
	if !m.db.Migrator().HasTable(&CloudMatchPlacement{}) {
		return out, nil
	}

	var placements []contestCountRow
	if err := m.db.DB.WithContext(ctx).Raw(`
		SELECT contest_id, COUNT(*) AS cnt
		FROM cloud_match_placements
		GROUP BY contest_id
	`).Scan(&placements).Error; err != nil {
		return nil, err
	}
	for _, row := range placements {
		out[row.ContestID] = append(out[row.ContestID], modules.ContestDataRow{
			Module: ModuleName,
			Label:  "match placements",
			Rows:   row.Cnt,
		})
	}

	// Scorecard values carry no contest key of their own: they are
	// reached through the placement they belong to.
	var values []contestCountRow
	if err := m.db.DB.WithContext(ctx).Raw(`
		SELECT p.contest_id, COUNT(*) AS cnt
		FROM cloud_scorecard_values v
		JOIN cloud_match_placements p ON p.id = v.cloud_match_placement_id
		GROUP BY p.contest_id
	`).Scan(&values).Error; err != nil {
		return nil, err
	}
	for _, row := range values {
		out[row.ContestID] = append(out[row.ContestID], modules.ContestDataRow{
			Module: ModuleName,
			Label:  "scorecard values",
			Rows:   row.Cnt,
		})
	}

	return out, nil
}

// ClearContestData implements modules.ContestDataClearer: it deletes
// every placement and scorecard value the cloud stored for one
// contest, and returns the number of rows removed.
//
// Scorecard values are deleted before the placements they reference,
// through a subquery so a concurrent push cannot leave values pointing
// at a placement that no longer exists. Postgres enforces the foreign
// key and SQLite does not, so this ordering is only observable on
// Postgres.
//
// Queries run on tx, the caller's transaction, which makes the
// cross-module wipe atomic.
func (m *Module) ClearContestData(ctx context.Context, tx *gorm.DB, contestID uint) (int64, error) {
	if contestID == 0 {
		return 0, errNoContest
	}
	if !tx.Migrator().HasTable(&CloudMatchPlacement{}) {
		return 0, nil
	}

	res := tx.WithContext(ctx).
		Where("cloud_match_placement_id IN (SELECT id FROM cloud_match_placements WHERE contest_id = ?)", contestID).
		Delete(&CloudScorecardValue{})
	if res.Error != nil {
		return 0, res.Error
	}
	values := res.RowsAffected

	res = tx.WithContext(ctx).
		Where("contest_id = ?", contestID).
		Delete(&CloudMatchPlacement{})
	if res.Error != nil {
		return values, res.Error
	}

	return values + res.RowsAffected, nil
}
