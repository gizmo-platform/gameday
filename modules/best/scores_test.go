package best

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func newTestModule(t *testing.T) (*Module, *db.DB) {
	t.Helper()

	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))

	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	ws, err := web.NewServer(web.WithDB(d))
	if err != nil {
		t.Fatalf("web.NewServer: %v", err)
	}

	tm := team.New(d, ws, nil)
	if tm == nil {
		t.Fatal("team.New failed")
	}
	if err := tm.Migrate(); err != nil {
		t.Fatalf("team Migrate: %v", err)
	}

	m := New(d, ws, nil)
	if m == nil {
		t.Fatal("best.New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("best Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/best")

	tm2 := team.Team{Name: "Test Team", Number: 42, Region: "NW"}
	if err := d.Create(&tm2).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	return m, d
}

func teamByNumber(t *testing.T, d *db.DB) team.Team {
	t.Helper()

	tm, err := gorm.G[team.Team](d.DB).Where(&team.Team{Number: 42}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup team: %v", err)
	}
	return tm
}

func scoreTypeForKey(t *testing.T, d *db.DB, key string) ScoreType {
	t.Helper()

	st, err := gorm.G[ScoreType](d.DB).Where(&ScoreType{Key: key}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup score type %q: %v", key, err)
	}
	return st
}

func countValues(t *testing.T, d *db.DB) int64 {
	t.Helper()

	var count int64
	if err := d.Model(&TeamScoreValue{}).Count(&count).Error; err != nil {
		t.Fatalf("count team score values: %v", err)
	}
	return count
}

func postScore(t *testing.T, m *Module, id, field, value string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	rctx.URLParams.Add("field", field)

	req := httptest.NewRequest("POST", "http://test.ui/ui/mod/best/scores/"+id+"/"+field,
		strings.NewReader(url.Values{"value": []string{value}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	m.uiViewScoreSet(w, req)
	return w
}

func TestMigrateSeedsScoreTypes(t *testing.T) {
	m, d := newTestModule(t)

	types, err := gorm.G[ScoreType](d.DB).Order("`order` ASC").Find(context.Background())
	if err != nil {
		t.Fatalf("find score types: %v", err)
	}

	want := []struct {
		key   string
		max   float32
		order int
	}{
		{"notebook", 300, 1},
		{"marketing", 250, 2},
		{"poster", 100, 3},
		{"video", 100, 4},
	}

	if len(types) != len(want) {
		t.Fatalf("expected %d score types, got %d", len(want), len(types))
	}
	for i, w := range want {
		if types[i].Key != w.key || types[i].Max != w.max || types[i].Order != w.order {
			t.Errorf("type %d: got %+v, want key=%s max=%v order=%d", i, types[i], w.key, w.max, w.order)
		}
	}

	// Migrate again: must be idempotent
	if err := m.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	types, err = gorm.G[ScoreType](d.DB).Order("`order` ASC").Find(context.Background())
	if err != nil {
		t.Fatalf("find score types after re-migrate: %v", err)
	}
	if len(types) != len(want) {
		t.Fatalf("after re-migrate: expected %d score types, got %d", len(want), len(types))
	}
}

func TestScoreSetUpsert(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	idStr := fmt.Sprintf("%d", tm.ID)
	st := scoreTypeForKey(t, d, "notebook")

	// Initial create
	w := postScore(t, m, idStr, "notebook", "150.5")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "?saved=") {
		t.Errorf("redirect Location missing ?saved=: %q", loc)
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected 1 value row, got %d", got)
	}
	v, err := gorm.G[TeamScoreValue](d.DB).
		Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
		First(context.Background())
	if err != nil {
		t.Fatalf("lookup value: %v", err)
	}
	if v.Value != 150.5 {
		t.Errorf("expected value 150.5, got %v", v.Value)
	}

	// Second post must upsert (not insert)
	w = postScore(t, m, idStr, "notebook", "200")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on upsert, got %d: %s", w.Code, w.Body.String())
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected still 1 value row after upsert, got %d", got)
	}
	v, err = gorm.G[TeamScoreValue](d.DB).
		Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
		First(context.Background())
	if err != nil {
		t.Fatalf("lookup value after upsert: %v", err)
	}
	if v.Value != 200 {
		t.Errorf("expected value 200 after upsert, got %v", v.Value)
	}
}

func TestScoreSetClear(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	st := scoreTypeForKey(t, d, "notebook")

	seed := TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID, Value: 10}
	if err := d.Create(&seed).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected 1 seeded value row, got %d", got)
	}

	w := postScore(t, m, fmt.Sprintf("%d", tm.ID), "notebook", "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on clear, got %d: %s", w.Code, w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected 0 value rows after clear, got %d", got)
	}
}

func TestScoreSetOutOfRange(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	idStr := fmt.Sprintf("%d", tm.ID)

	w := postScore(t, m, idStr, "notebook", "99999")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for out-of-range value, got %d", got)
	}

	// Negative values are also rejected
	w = postScore(t, m, idStr, "notebook", "-5")
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page for negative value, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for negative value, got %d", got)
	}
}

func TestScoreSetUnknownField(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)

	w := postScore(t, m, fmt.Sprintf("%d", tm.ID), "bogus", "10")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for unknown field, got %d", got)
	}
}

func TestScoresRender(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	st := scoreTypeForKey(t, d, "notebook")
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID, Value: 150.5}).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/", nil)
	w := httptest.NewRecorder()
	m.uiViewScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		"/static/css/scores.css",
		"Test Team",
		"<th class=\"has-text-right\">Total</th>",
		"value=\"150.50\"",
		"Total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}

	// One empty cell must render the em-dash placeholder
	if !strings.Contains(body, `placeholder="—"`) {
		t.Errorf("rendered page missing empty-cell placeholder")
	}

	if got := strings.Count(body, "score-input"); got != 4 {
		t.Errorf("expected 4 score inputs (one per score type), got %d", got)
	}

	// Server-computed form actions must be well-formed (no double slash)
	if strings.Contains(body, "action=\"//") || strings.Contains(body, "scores//") {
		t.Errorf("rendered page contains a double-slash form action")
	}
	if !strings.Contains(body, fmt.Sprintf("action=\"/ui/mod/best/scores/%d/notebook\"", tm.ID)) {
		t.Errorf("rendered page missing expected form action for team %d", tm.ID)
	}
}

func TestDebugGenerateScores(t *testing.T) {
	m, d := newTestModule(t)

	if err := m.DebugGenerateScores(); err != nil {
		t.Fatalf("DebugGenerateScores: %v", err)
	}

	tm := teamByNumber(t, d)
	types, err := gorm.G[ScoreType](d.DB).Find(context.Background())
	if err != nil {
		t.Fatalf("find score types: %v", err)
	}

	if got := countValues(t, d); got != int64(len(types)) {
		t.Fatalf("expected %d generated value rows, got %d", len(types), got)
	}

	for _, st := range types {
		v, err := gorm.G[TeamScoreValue](d.DB).
			Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
			First(context.Background())
		if err != nil {
			t.Fatalf("missing generated value for type %s: %v", st.Key, err)
		}
		if v.Value < 0 || v.Value > st.Max {
			t.Errorf("generated value %v for type %s out of range [0, %v]", v.Value, st.Key, st.Max)
		}
	}

	// Running again must replace, not duplicate
	if err := m.DebugGenerateScores(); err != nil {
		t.Fatalf("second DebugGenerateScores: %v", err)
	}
	if got := countValues(t, d); got != int64(len(types)) {
		t.Fatalf("after re-generation expected %d rows, got %d", len(types), got)
	}
}
