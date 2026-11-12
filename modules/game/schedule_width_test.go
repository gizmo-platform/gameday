package game

import (
	"strings"
	"testing"
)

func TestWidthOptions(t *testing.T) {
	tests := []struct {
		name      string
		teams     int
		fields    int
		positions int
		// wantValues lists the expected option values in order.
		wantValues []int
	}{
		{
			name:       "exact fit offers only automatic",
			teams:      12,
			fields:     1,
			positions:  4,
			wantValues: []int{0},
		},
		{
			name:       "ragged field offers explicit full width before narrower ones",
			teams:      9,
			fields:     1,
			positions:  4,
			wantValues: []int{0, 4, 3, 2},
		},
		{
			name:       "single position offers nothing to narrow to",
			teams:      9,
			fields:     1,
			positions:  1,
			wantValues: []int{0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := widthOptions(tc.teams, tc.fields, tc.positions)
			if len(rows) != len(tc.wantValues) {
				t.Fatalf("got %d options, want %d: %+v", len(rows), len(tc.wantValues), rows)
			}
			for i, row := range rows {
				if row.Value != tc.wantValues[i] {
					t.Errorf("option %d: got value %d, want %d", i, row.Value, tc.wantValues[i])
				}
			}
			full := rows[0]
			if !strings.EqualFold(full.Label, "Automatic") {
				t.Errorf("first option should be bare Automatic, got %q", full.Label)
			}
			for _, row := range rows[1:] {
				if row.Value == tc.positions && !strings.Contains(row.Label, "always full matches") {
					t.Errorf("explicit full-width option should promise full matches, got %q", row.Label)
				}
			}
		})
	}
}
