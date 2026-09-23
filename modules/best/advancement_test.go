package best

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// seedBestScores creates additional teams and per-type score values,
// returning the full roster of teams in the test module.  The harness
// team 42 has no scores on purpose.
func seedBestScores(t *testing.T, d *db.DB, specs map[int]map[string]float32) map[uint]team.Team {
	t.Helper()

	roster := make(map[uint]team.Team)
	existing, err := gorm.G[team.Team](d.DB).Find(context.Background())
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	for _, team := range existing {
		roster[team.ID] = team
	}

	for number, scores := range specs {
		team := team.Team{Name: fmt.Sprintf("Team %d", number), Number: number}
		if err := d.Create(&team).Error; err != nil {
			t.Fatalf("create team %d: %v", number, err)
		}
		roster[team.ID] = team
		for key, value := range scores {
			st := scoreTypeForKey(t, d, key)
			sv := TeamScoreValue{TeamID: team.ID, ScoreTypeID: st.ID, Value: value}
			if err := d.Create(&sv).Error; err != nil {
				t.Fatalf("create %s value for team %d: %v", key, number, err)
			}
		}
	}

	return roster
}

// TestNotebookAdvancementInclude checks that the filter advances the top
// N teams by notebook score.  Ranks: 1: team 100 (300), 2: team 200
// (250, poster 90), 3: team 300 (250, poster 70), 4: team 400 (100).
// Team 42 has no scores.
func TestNotebookAdvancementInclude(t *testing.T) {
	_, d := newTestModule(t)
	roster := seedBestScores(t, d, map[int]map[string]float32{
		100: {"notebook": 300},
		200: {"notebook": 250, "poster": 90},
		300: {"notebook": 250, "poster": 70},
		400: {"notebook": 100},
	})

	sctx := game.AdvancementFilterContext{
		Roster:     roster,
		Candidates: make(map[uint]team.Team),
	}

	nb := &NotebookAdvancement{db: d}
	if err := nb.Apply(&sctx, "3", game.GamePhaseAdvancementFilterModeInclude, "3"); err != nil {
		t.Fatalf("Apply include: %v", err)
	}

	byNumber := make(map[int]team.Team)
	for id, team := range sctx.Candidates {
		byNumber[team.Number] = team
		if id != team.ID {
			t.Errorf("candidate key %d does not match team ID %d", id, team.ID)
		}
	}

	if len(byNumber) != 3 {
		t.Fatalf("expected 3 advancing teams, got %d: %v", len(byNumber), byNumber)
	}
	for _, number := range []int{100, 200, 300} {
		if _, ok := byNumber[number]; !ok {
			t.Errorf("expected team %d in candidates", number)
		}
	}

	accepted := make(map[int]struct{})
	rejected := make(map[int]struct{})
	for _, det := range sctx.Determinations {
		if det.Filter != AdvancementFilterBESTNotebook {
			t.Errorf("determination filter %q, want %q", det.Filter, AdvancementFilterBESTNotebook)
		}
		if det.Rule != "3" {
			t.Errorf("determination rule %q, want %q", det.Rule, "3")
		}
		switch det.Result {
		case game.AdvancementDeterminationAccept:
			accepted[det.Team.Number] = struct{}{}
		case game.AdvancementDeterminationReject:
			rejected[det.Team.Number] = struct{}{}
		}
	}
	if len(accepted) != 3 {
		t.Errorf("expected 3 accepted determinations, got %d: %v", len(accepted), accepted)
	}
	for _, number := range []int{42, 400} {
		if _, ok := rejected[number]; !ok {
			t.Errorf("expected team %d to be rejected", number)
		}
	}
}

// TestNotebookAdvancementExclude checks that the filter removes the top N
// teams from the candidate set.
func TestNotebookAdvancementExclude(t *testing.T) {
	_, d := newTestModule(t)
	roster := seedBestScores(t, d, map[int]map[string]float32{
		100: {"notebook": 300},
		200: {"notebook": 250, "poster": 90},
		300: {"notebook": 250, "poster": 70},
		400: {"notebook": 100},
	})

	sctx := game.AdvancementFilterContext{
		Roster:     roster,
		Candidates: make(map[uint]team.Team),
	}
	maps.Copy(sctx.Candidates, roster)

	nb := &NotebookAdvancement{db: d}
	if err := nb.Apply(&sctx, "3", game.GamePhaseAdvancementFilterModeExclude, "3"); err != nil {
		t.Fatalf("Apply exclude: %v", err)
	}

	removed := make(map[int]struct{})
	for id, team := range roster {
		if _, ok := sctx.Candidates[id]; !ok {
			removed[team.Number] = struct{}{}
		}
	}

	if len(removed) != 3 {
		t.Fatalf("expected 3 teams removed, got %d: %v", len(removed), removed)
	}
	for _, number := range []int{100, 200, 300} {
		if _, ok := removed[number]; !ok {
			t.Errorf("expected team %d removed from candidates", number)
		}
	}
}

func TestNotebookAdvancementBadExpression(t *testing.T) {
	_, d := newTestModule(t)
	roster := seedBestScores(t, d, map[int]map[string]float32{100: {"notebook": 300}})

	sctx := game.AdvancementFilterContext{
		Roster:     roster,
		Candidates: make(map[uint]team.Team),
	}

	nb := &NotebookAdvancement{db: d}
	if err := nb.Apply(&sctx, "2", game.GamePhaseAdvancementFilterModeInclude, "Scoreboard"); err == nil {
		t.Fatal("expected error for non-integer cutoff expression")
	}
}

// TestNotebookAdvancementTieBreakers checks the full tie breaking chain:
// marketing, then poster, then video, then team number.  All teams tie
// on the notebook score.
func TestNotebookAdvancementTieBreakers(t *testing.T) {
	_, d := newTestModule(t)
	roster := seedBestScores(t, d, map[int]map[string]float32{
		// Rank 5: lowest marketing score ranks last.
		100: {"notebook": 200, "poster": 50, "marketing": 0, "video": 0},
		// Rank 3: tied with 400 on marketing and poster, lower video
		// ranks after.
		200: {"notebook": 200, "poster": 50, "marketing": 80, "video": 0},
		// Rank 1: highest poster among the marketing-80 team.
		300: {"notebook": 200, "poster": 90, "marketing": 80, "video": 0},
		// Rank 2: tied with 200 on marketing and poster, higher video
		// wins.
		400: {"notebook": 200, "poster": 50, "marketing": 80, "video": 20},
		// Rank 4: all tie breakers tied with 600, lower number wins
		// the last-resort tie break.
		500: {"notebook": 200, "poster": 10, "marketing": 10, "video": 10},
		600: {"notebook": 200, "poster": 10, "marketing": 10, "video": 10},
	})

	sctx := game.AdvancementFilterContext{
		Roster:     roster,
		Candidates: make(map[uint]team.Team),
	}

	nb := &NotebookAdvancement{db: d}
	if err := nb.Apply(&sctx, "1", game.GamePhaseAdvancementFilterModeInclude, "4"); err != nil {
		t.Fatalf("Apply include: %v", err)
	}

	byNumber := make(map[int]team.Team)
	for _, team := range sctx.Candidates {
		byNumber[team.Number] = team
	}
	if len(byNumber) != 5 {
		t.Fatalf("expected 5 advancing teams, got %d: %v", len(byNumber), byNumber)
	}
	for _, number := range []int{300, 200, 400, 500, 600} {
		if _, ok := byNumber[number]; !ok {
			t.Errorf("expected team %d in candidates", number)
		}
	}
	for _, number := range []int{100} {
		if _, ok := byNumber[number]; ok {
			t.Errorf("expected team %d to be rejected", number)
		}
	}

	// Ranks must follow the full tie breaking chain.  All teams tie on
	// notebook 200, so the marketing, poster, and video scores (then
	// team number) decide the order:
	//   300 (80,90,0)    rank 1
	//   400 (80,50,20)   rank 2
	//   200 (80,50,0)    rank 3
	//   500 (10,10,10)   rank 4 (tied with 600)
	//   600 (10,10,10)   rank 4
	//   100 (0,50,0)     rank 5
	ranks := make(map[int]int)
	for _, det := range sctx.Determinations {
		if !strings.Contains(det.Reason, "Notebook rank") {
			continue
		}
		rank := 0
		if i := strings.Index(det.Reason, "("); i >= 0 {
			s := det.Reason[i+1:]
			for j := 0; j < len(s) && s[j] >= '0' && s[j] <= '9'; j++ {
				rank = rank*10 + int(s[j]-'0')
			}
		} else {
			t.Errorf("could not parse rank from %q", det.Reason)
			continue
		}
		ranks[det.Team.Number] = rank
	}
	for number, want := range map[int]int{300: 1, 400: 2, 200: 3, 500: 4, 600: 4, 100: 5} {
		if got := ranks[number]; got != want {
			t.Errorf("expected team %d at rank %d, got %d", number, want, got)
		}
	}
}

// TestNotebookTieBreaker checks the registered tie-breaker orders tied
// teams by notebook score first, then marketing, then poster, then
// video (each higher first), then team number.
func TestNotebookTieBreaker(t *testing.T) {
	_, d := newTestModule(t)
	seedBestScores(t, d, map[int]map[string]float32{
		// Position 1: strictly highest notebook beats every other
		// criterion.
		100: {"notebook": 300, "poster": 50, "marketing": 0, "video": 0},
		// Position 6: notebook tied with 400, lowest marketing
		// score ranks last.
		200: {"notebook": 250, "poster": 80, "marketing": 0, "video": 0},
		// Position 3: notebook and marketing tied with 400, lower
		// video ranks after.
		300: {"notebook": 250, "poster": 80, "marketing": 40, "video": 0},
		// Position 2: notebook and marketing tied with 300, higher
		// video wins.
		400: {"notebook": 250, "poster": 80, "marketing": 40, "video": 20},
		// Position 4: all tie breakers equal with 600, lower team
		// number wins the last-resort tie break.
		500: {"notebook": 250, "poster": 10, "marketing": 10, "video": 10},
		600: {"notebook": 250, "poster": 10, "marketing": 10, "video": 10},
	})

	tb, ok := modules.GetTieBreaker(TieBreakerBESTUnified)
	if !ok {
		t.Fatalf("tie-breaker %q not registered", TieBreakerBESTUnified)
	}

	in := []int{400, 600, 100, 300, 500, 200}
	got := tb(in, 0)

	want := []int{100, 400, 300, 500, 600, 200}
	if len(got) != len(want) {
		t.Fatalf("expected %d ordered teams, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: expected team %d, got %d (full order %v)", i, want[i], got[i], got)
		}
	}

	seen := make(map[int]int)
	for _, number := range got {
		seen[number]++
	}
	if len(seen) != len(in) {
		t.Errorf("expected %d distinct teams in output, got %d: %v", len(in), len(seen), got)
	}
	for _, count := range seen {
		if count != 1 {
			t.Errorf("team appears %d times in output", count)
		}
	}
}
