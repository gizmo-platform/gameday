package game

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/cloud"
)

// pushRecorder is a fake cloud contest-scores endpoint that records
// every push it receives.  Its state is guarded by a mutex because the
// pushes arrive from the background worker.  When gate is non-nil each
// request blocks until gate is closed; started receives a value as
// each request arrives.
type pushRecorder struct {
	mu       sync.Mutex
	payloads [][]cloud.ContestPlacement
	status   int

	gate    chan struct{}
	started chan struct{}
}

func (p *pushRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []cloud.ContestPlacement
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode push body: %v", err)
		}
		p.mu.Lock()
		p.payloads = append(p.payloads, body)
		status := p.status
		p.mu.Unlock()

		if p.started != nil {
			p.started <- struct{}{}
		}
		if p.gate != nil {
			<-p.gate
		}
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "cloud unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"stored": len(body)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *pushRecorder) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.payloads)
}

// submitTestScorecard submits a scorecard for mp with the given
// widgets_fabricated count, returning the response.
func submitTestScorecard(t *testing.T, m *Module, mp MatchPlacement, widgets int) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("widgets_fabricated", fmt.Sprint(widgets))
	return submitScorecardForm(t, m, mp, form)
}

// submitScorecardForm submits a scorecard for mp with an arbitrary
// form, returning the response.
func submitScorecardForm(t *testing.T, m *Module, mp MatchPlacement, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("phase", fmt.Sprint(mp.PhaseID))
	rctx.URLParams.Add("match", fmt.Sprint(mp.Match))
	rctx.URLParams.Add("field", fmt.Sprint(mp.FieldID))
	rctx.URLParams.Add("position", fmt.Sprint(mp.PositionID))

	req := httptest.NewRequest("POST", "http://test.ui/ui/mod/game/scorecard/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	m.uiViewScorecardSubmit(w, req)
	return w
}

// seedScorableContest seeds contest data for contestID (0 seeds no
// contest selection) and the scorecard element, returning the
// placement to submit against.
func seedScorableContest(t *testing.T, m *Module, contestID uint) MatchPlacement {
	t.Helper()
	d := m.db
	if contestID != 0 {
		seedContestData(t, d, contestID)
	} else {
		seedContestData(t, d, 7)
		if err := d.Exec("DELETE FROM onsite_contests").Error; err != nil {
			t.Fatalf("clear contest selection: %v", err)
		}
	}
	sce := ScorecardElement{Element: "widgets_fabricated", Type: "count", Expr: "int(widgets_fabricated * 1)"}
	if err := d.Create(&sce).Error; err != nil {
		t.Fatalf("create scorecard element: %v", err)
	}
	mp, err := gorm.G[MatchPlacement](d.DB).First(context.Background())
	if err != nil {
		t.Fatalf("load placement: %v", err)
	}
	return mp
}

// TestScorecardSubmitPushesToCloud verifies a scorecard submit pushes
// the contest's data, including the new score, to the cloud.
func TestScorecardSubmitPushesToCloud(t *testing.T) {
	rec := &pushRecorder{}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, _ := newTestModule(t)
	mp := seedScorableContest(t, m, 7)

	w := submitTestScorecard(t, m, mp, 9)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("submit = %d, want 303", w.Code)
	}
	m.autoPush.wg.Wait()

	if rec.count() != 1 {
		t.Fatalf("pushes = %d, want 1", rec.count())
	}
	got := rec.payloads[0]
	if len(got) != 1 || got[0].Score != 9 {
		t.Fatalf("pushed payload = %+v, want one placement scoring 9", got)
	}
	if len(got[0].Values) != 1 || got[0].Values[0].Value != 9 {
		t.Errorf("pushed values = %+v, want widgets_fabricated=9", got[0].Values)
	}
}

// TestScorecardSubmitNoContestSkipsPush verifies nothing is pushed when
// no contest is selected on this server.
func TestScorecardSubmitNoContestSkipsPush(t *testing.T) {
	rec := &pushRecorder{}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, _ := newTestModule(t)
	mp := seedScorableContest(t, m, 0)

	if w := submitTestScorecard(t, m, mp, 3); w.Code != http.StatusSeeOther {
		t.Fatalf("submit = %d, want 303", w.Code)
	}
	m.autoPush.wg.Wait()

	if rec.count() != 0 {
		t.Errorf("pushes = %d, want 0 with no contest selected", rec.count())
	}
}

// TestScorecardSubmitCloudFailureIsBestEffort verifies a failing cloud
// neither fails the submit nor loses the locally saved score.
func TestScorecardSubmitCloudFailureIsBestEffort(t *testing.T) {
	rec := &pushRecorder{status: http.StatusInternalServerError}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, d := newTestModule(t)
	mp := seedScorableContest(t, m, 7)

	if w := submitTestScorecard(t, m, mp, 4); w.Code != http.StatusSeeOther {
		t.Fatalf("submit = %d, want 303", w.Code)
	}
	m.autoPush.wg.Wait()

	if rec.count() != 1 {
		t.Errorf("pushes = %d, want 1 attempt", rec.count())
	}
	score, err := gorm.G[MatchScore](d.DB).Where(&MatchScore{ID: mp.ID}).First(context.Background())
	if err != nil {
		t.Fatalf("load score: %v", err)
	}
	if score.Score != 4 {
		t.Errorf("saved score = %d, want 4", score.Score)
	}
}

// TestScorecardSubmitPushesCoalesce verifies pushes never overlap and
// that submits arriving during a push collapse into one follow-up push
// carrying the latest data.
func TestScorecardSubmitPushesCoalesce(t *testing.T) {
	rec := &pushRecorder{gate: make(chan struct{}), started: make(chan struct{}, 8)}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, _ := newTestModule(t)
	mp := seedScorableContest(t, m, 7)

	submitTestScorecard(t, m, mp, 1)
	<-rec.started // the first push is in flight and blocked

	for i := 2; i <= 5; i++ {
		submitTestScorecard(t, m, mp, i)
	}
	close(rec.gate)
	m.autoPush.wg.Wait()

	if rec.count() != 2 {
		t.Fatalf("pushes = %d, want 2 (one in flight, one coalesced follow-up)", rec.count())
	}
	if last := rec.payloads[1]; len(last) != 1 || last[0].Score != 5 {
		t.Errorf("follow-up push = %+v, want the latest score 5", last)
	}
}

// assertScorecardRejected checks a failed submit rendered the error
// page containing want instead of redirecting, rolled back so the
// seeded score of 42 and widgets value of 5 survive, and pushed
// nothing.
func assertScorecardRejected(t *testing.T, m *Module, rec *pushRecorder, mp MatchPlacement, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	m.autoPush.wg.Wait()

	if w.Code == http.StatusSeeOther {
		t.Fatalf("failed submit redirected to %q", w.Header().Get("Location"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "Internal Error") || !strings.Contains(body, want) {
		t.Errorf("error page missing %q:\n%s", want, body)
	}

	ctx := context.Background()
	score, err := gorm.G[MatchScore](m.db.DB).Where(&MatchScore{ID: mp.ID}).First(ctx)
	if err != nil {
		t.Fatalf("previous score lost: %v", err)
	}
	if score.Score != 42 {
		t.Errorf("score = %d, want the previous 42 (rolled back)", score.Score)
	}
	values, err := gorm.G[ScorecardValue](m.db.DB).Where(&ScorecardValue{MatchPlacementID: mp.ID}).Find(ctx)
	if err != nil {
		t.Fatalf("load values: %v", err)
	}
	if len(values) != 1 || values[0].Value != 5 {
		t.Errorf("values = %+v, want the previous widgets_fabricated=5", values)
	}
	if rec.count() != 0 {
		t.Errorf("pushes = %d, want 0 after a failed submit", rec.count())
	}
}

// TestScorecardSubmitInvalidValue verifies a non-numeric form value
// shows the error page, saves nothing, and does not redirect.
func TestScorecardSubmitInvalidValue(t *testing.T) {
	rec := &pushRecorder{}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, _ := newTestModule(t)
	mp := seedScorableContest(t, m, 7)

	w := submitScorecardForm(t, m, mp, url.Values{"widgets_fabricated": {"lots"}})
	assertScorecardRejected(t, m, rec, mp, w, "invalid value &quot;lots&quot; for widgets_fabricated")
}

// TestScorecardSubmitNonIntegerScore verifies a scorecard expression
// that does not produce an integer is reported instead of panicking.
func TestScorecardSubmitNonIntegerScore(t *testing.T) {
	rec := &pushRecorder{}
	t.Setenv("GAMEDAY_CLOUD", rec.server(t).URL)
	m, d := newTestModule(t)
	mp := seedScorableContest(t, m, 7)
	if err := d.Model(&ScorecardElement{}).Where("element = ?", "widgets_fabricated").Update("expr", "widgets_fabricated * 1.5").Error; err != nil {
		t.Fatalf("update expr: %v", err)
	}

	w := submitTestScorecard(t, m, mp, 3)
	assertScorecardRejected(t, m, rec, mp, w, "want an integer")
}
