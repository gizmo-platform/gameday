package modules

import (
	"context"

	"gorm.io/gorm"
)

// ContestDataRow is one labelled count of the data a module holds for
// a single contest, for display in the clear-confirmation preview.
type ContestDataRow struct {
	Module string
	Label  string
	Rows   int64
}

// ContestDataClearer is the optional interface a module implements to
// let the contest screen erase everything tied to one contest. It is
// discovered by type assertion from the resolved ModuleDeps, so a
// module with no contest-scoped data simply does not implement it.
//
// It lives in the modules package rather than in the contest package
// (where the consumer-side interface convention would normally put it,
// cf. contest.TeamModule) because its signature returns a type the
// game module must reference, and game cannot import contest: contest
// already imports game. game deliberately reads contest tables through
// TableName-overridden shadow structs to avoid that cycle, and this
// contract is the same seam, inverted.
type ContestDataClearer interface {
	// ContestDataCounts reports the rows this module holds for every
	// contest, keyed by contest ID, for the confirmation preview. It
	// is called on every contest-list render, so it must be a single
	// grouped query per table, not a query per contest. A table that
	// does not exist yet contributes no rows rather than an error.
	ContestDataCounts(ctx context.Context) (map[uint][]ContestDataRow, error)

	// ClearContestData deletes this module's rows for one contest and
	// returns the number removed. All queries must go through tx, so
	// the caller can make the cross-module wipe atomic. Missing data
	// is not an error: clearing an already-empty contest returns 0.
	ClearContestData(ctx context.Context, tx *gorm.DB, contestID uint) (int64, error)
}
