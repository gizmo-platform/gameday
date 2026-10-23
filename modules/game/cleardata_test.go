package game

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
)

// countCloudRows reports how many placements and values the database
// holds for one contest, so tests can assert exactly what a clear
// removed and what it left alone.
func countCloudRows(t *testing.T, d *db.DB, contestID uint) (placements int64, values int64) {
	t.Helper()

	if err := d.Raw().Raw("SELECT COUNT(*) FROM cloud_match_placements WHERE contest_id = ?", contestID).Scan(&placements).Error; err != nil {
		t.Fatalf("count placements: %v", err)
	}
	if err := d.Raw().Raw(`SELECT COUNT(*) FROM cloud_scorecard_values v
		JOIN cloud_match_placements p ON p.id = v.cloud_match_placement_id
		WHERE p.contest_id = ?`, contestID).Scan(&values).Error; err != nil {
		t.Fatalf("count values: %v", err)
	}
	return placements, values
}

// countAllCloudValues reports every stored scorecard value, including
// ones whose placement is gone, which is how a test detects orphans.
func countAllCloudValues(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var n int64
	if err := d.Raw().Raw("SELECT COUNT(*) FROM cloud_scorecard_values").Scan(&n).Error; err != nil {
		t.Fatalf("count values: %v", err)
	}
	return n
}

func TestContestDataCounts(t *testing.T) {
	m, d := newTestModule(t)

	// Contest 7 has two placements with two values each; contest 9 has
	// one placement with no values; contest 42 has nothing and must not
	// appear at all.
	for i := 1; i <= 2; i++ {
		p := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
			ContestID: 7, Round: 1, Match: i, PhaseID: 1,
			TeamID: uint(100 + i), FieldID: 1, PositionID: 1,
		})
		seedCloudValue(t, d.DB, p.ID, "EID_1", 1)
		seedCloudValue(t, d.DB, p.ID, "EID_2", 2)
	}
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 9, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 200, FieldID: 1, PositionID: 1,
	})

	got, err := m.ContestDataCounts(context.Background())
	if err != nil {
		t.Fatalf("ContestDataCounts: %v", err)
	}

	if rows := got[7]; len(rows) != 2 {
		t.Errorf("contest 7 rows = %d, want 2: %+v", len(rows), rows)
	} else {
		if rows[0].Label != "match placements" || rows[0].Rows != 2 {
			t.Errorf("contest 7 placements = %+v, want 2 match placements", rows[0])
		}
		if rows[1].Label != "scorecard values" || rows[1].Rows != 4 {
			t.Errorf("contest 7 values = %+v, want 4 scorecard values", rows[1])
		}
		for _, row := range rows {
			if row.Module != ModuleName {
				t.Errorf("row %+v module = %q, want %q", row, row.Module, ModuleName)
			}
		}
	}

	rows9 := got[9]
	if len(rows9) != 1 || rows9[0].Label != "match placements" || rows9[0].Rows != 1 {
		t.Errorf("contest 9 rows = %+v, want one placement count of 1", rows9)
	}

	if _, ok := got[42]; ok {
		t.Errorf("contest 42 present in counts: %+v", got[42])
	}
}

func TestContestDataCountsNoTables(t *testing.T) {
	// A database where the cloud tables have not been created yet must
	// yield an empty result, not an error: the contest list page stays
	// usable regardless of migration state.
	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "empty.db"))
	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	m := &Module{db: d}

	got, err := m.ContestDataCounts(context.Background())
	if err != nil {
		t.Fatalf("ContestDataCounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("counts = %+v, want empty", got)
	}
}

func TestClearContestData(t *testing.T) {
	m, d := newTestModule(t)

	for i := 1; i <= 2; i++ {
		p := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
			ContestID: 7, Round: 1, Match: i, PhaseID: 1,
			TeamID: uint(100 + i), FieldID: 1, PositionID: 1,
		})
		seedCloudValue(t, d.DB, p.ID, "EID_1", 1)
		seedCloudValue(t, d.DB, p.ID, "EID_2", 2)
	}
	other := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 9, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 200, FieldID: 1, PositionID: 1,
	})
	seedCloudValue(t, d.DB, other.ID, "EID_1", 5)

	rows, err := m.ClearContestData(context.Background(), d.DB, 7)
	if err != nil {
		t.Fatalf("ClearContestData: %v", err)
	}
	if rows != 6 {
		t.Errorf("removed = %d, want 6 (2 placements + 4 values)", rows)
	}

	if p, v := countCloudRows(t, d, 7); p != 0 || v != 0 {
		t.Errorf("contest 7 still has %d placements, %d values", p, v)
	}
	if p, v := countCloudRows(t, d, 9); p != 1 || v != 1 {
		t.Errorf("contest 9 = %d placements, %d values, want 1 and 1", p, v)
	}
	if n := countAllCloudValues(t, d); n != 1 {
		t.Errorf("orphaned values: %d total remaining, want 1", n)
	}
}

func TestClearContestDataEmptyContest(t *testing.T) {
	m, d := newTestModule(t)

	rows, err := m.ClearContestData(context.Background(), d.DB, 42)
	if err != nil {
		t.Fatalf("ClearContestData: %v", err)
	}
	if rows != 0 {
		t.Errorf("removed = %d, want 0", rows)
	}
}

func TestClearContestDataNoContest(t *testing.T) {
	m, d := newTestModule(t)

	if _, err := m.ClearContestData(context.Background(), d.DB, 0); !errors.Is(err, errNoContest) {
		t.Errorf("error = %v, want errNoContest", err)
	}
}

func TestClearContestDataNoTables(t *testing.T) {
	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "empty.db"))
	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	m := &Module{db: d}

	rows, err := m.ClearContestData(context.Background(), d.DB, 7)
	if err != nil {
		t.Fatalf("ClearContestData: %v", err)
	}
	if rows != 0 {
		t.Errorf("removed = %d, want 0", rows)
	}
}

func TestClearContestDataRollback(t *testing.T) {
	// A clearer failing part-way through must leave the contest's data
	// untouched, which is what running inside the caller's transaction
	// buys.
	m, d := newTestModule(t)

	p := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 7, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 100, FieldID: 1, PositionID: 1,
	})
	seedCloudValue(t, d.DB, p.ID, "EID_1", 1)

	sentinel := errors.New("boom")
	err := d.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := m.ClearContestData(context.Background(), tx, 7); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("transaction error = %v, want sentinel", err)
	}

	if p, v := countCloudRows(t, d, 7); p != 1 || v != 1 {
		t.Errorf("rolled-back contest 7 = %d placements, %d values, want 1 and 1", p, v)
	}
}
