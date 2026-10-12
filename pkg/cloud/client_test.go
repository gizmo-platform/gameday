package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoDecodesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Errorf("path = %q, want /ping", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": 7})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	var out struct {
		OK bool `json:"ok"`
		N  int  `json:"n"`
	}
	if err := c.Do(context.Background(), http.MethodGet, "/ping", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !out.OK || out.N != 7 {
		t.Errorf("decoded = %+v, want ok=true n=7", out)
	}
}

func TestDoEncodesQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("region"); got != "New York" {
			t.Errorf("region = %q, want New York", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	query := map[string][]string{"region": {"New York"}}
	if err := c.Do(context.Background(), http.MethodGet, "/teams", query, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDoNon2xxReturnsCloudError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("  boom\n"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("Do: expected error, got nil")
	}
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %T (%v), want *cloud.Error", err, err)
	}
	if ce.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", ce.Status)
	}
	if ce.Message != "boom" {
		t.Errorf("Message = %q, want boom", ce.Message)
	}
}

func TestDoNetworkFailureIsNotCloudError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	c := New(WithBaseURL(server.URL))
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("Do: expected error, got nil")
	}
	var ce *Error
	if errors.As(err, &ce) {
		t.Errorf("err = %v, want a transport error, not *cloud.Error", err)
	}
}

func TestNewUsesEnvBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("null"))
	}))
	defer server.Close()

	t.Setenv("GAMEDAY_CLOUD", server.URL+"/")
	c := New()
	if got := c.BaseURL(); got != server.URL {
		t.Errorf("BaseURL() = %q, want %q", got, server.URL)
	}
}

func TestNewDefaultsToCanonicalCloud(t *testing.T) {
	t.Setenv("GAMEDAY_CLOUD", "")
	c := New()
	if got := c.BaseURL(); got != DefaultBaseURL {
		t.Errorf("BaseURL() = %q, want %q", got, DefaultBaseURL)
	}
}

func TestWithBaseURLTrimsTrailingSlash(t *testing.T) {
	c := New(WithBaseURL("http://example.test/"))
	if got := c.BaseURL(); got != "http://example.test" {
		t.Errorf("BaseURL() = %q, want http://example.test", got)
	}
}

func TestBasicAuthSetsHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("null"))
	}))
	defer server.Close()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	c := New(WithBaseURL(server.URL), WithBasicAuth("user", "secret"))
	if err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestUnauthenticatedClientSendsNoAuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("null"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	if err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", gotAuth)
	}
}

func TestGetGameFileRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != gameSetupPath {
			t.Errorf("path = %q, want %s", r.URL.Path, gameSetupPath)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte("Field:\n  Positions:\n    - ID: 1\n      Name: red\n"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	body, err := c.GetGameFile(context.Background())
	if err != nil {
		t.Fatalf("GetGameFile: %v", err)
	}
	want := "Field:\n  Positions:\n    - ID: 1\n      Name: red\n"
	if string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestGetGameFileNon2xxReturnsCloudError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no game file"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	_, err := c.GetGameFile(context.Background())
	if err == nil {
		t.Fatal("GetGameFile: expected error, got nil")
	}
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %T (%v), want *cloud.Error", err, err)
	}
	if ce.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", ce.Status)
	}
	if ce.Message != "no game file" {
		t.Errorf("Message = %q, want no game file", ce.Message)
	}
}

func TestGetRegionsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != teamRegionsPath {
			t.Errorf("path = %q, want %s", r.URL.Path, teamRegionsPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]RegionInfo{
			{Name: "North", TeamCount: 3},
			{Name: "South", TeamCount: 0},
		})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	regions, err := c.GetRegions(context.Background())
	if err != nil {
		t.Fatalf("GetRegions: %v", err)
	}
	if len(regions) != 2 {
		t.Fatalf("len(regions) = %d, want 2", len(regions))
	}
	if regions[0].Name != "North" || regions[0].TeamCount != 3 {
		t.Errorf("regions[0] = %+v, want North/3", regions[0])
	}
	if regions[1].Name != "South" || regions[1].TeamCount != 0 {
		t.Errorf("regions[1] = %+v, want South/0", regions[1])
	}
}

func TestGetRegionTeamsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != teamTeamsPath {
			t.Errorf("path = %q, want %s", r.URL.Path, teamTeamsPath)
		}
		if got := r.URL.Query().Get("region"); got != "North" {
			t.Errorf("region = %q, want North", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TeamInfo{
			{Number: 1, Name: "Alpha", Division: "Open", Region: "North"},
		})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	teams, err := c.GetRegionTeams(context.Background(), "North")
	if err != nil {
		t.Fatalf("GetRegionTeams: %v", err)
	}
	if len(teams) != 1 {
		t.Fatalf("len(teams) = %d, want 1", len(teams))
	}
	want := TeamInfo{Number: 1, Name: "Alpha", Division: "Open", Region: "North"}
	if teams[0] != want {
		t.Errorf("teams[0] = %+v, want %+v", teams[0], want)
	}
}

func TestDoJSONRoundTrip(t *testing.T) {
	var gotPath, gotMethod, gotContentType string
	var gotBody []ContestPlacement
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"stored": 1})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	body := []ContestPlacement{{
		Round: 1, Match: 1, PhaseID: 1, TeamID: 1, TeamName: "Team 1",
		FieldID: 1, PositionID: 1, State: 4, Score: 42,
		Values: []ContestScorecardValue{{Element: "widgets_fabricated", Value: 5}},
	}}
	var out struct {
		Stored int `json:"stored"`
	}
	if err := c.DoJSON(context.Background(), http.MethodPost, "/x", nil, body, &out); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if gotPath != "/x" || gotMethod != http.MethodPost {
		t.Errorf("request = %s %s, want POST /x", gotMethod, gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if len(gotBody) != 1 {
		t.Fatalf("request body = %d placements, want 1", len(gotBody))
	}
	got := gotBody[0]
	want := body[0]
	if got.Round != want.Round || got.Match != want.Match || got.PhaseID != want.PhaseID ||
		got.TeamID != want.TeamID || got.TeamName != want.TeamName || got.FieldID != want.FieldID ||
		got.PositionID != want.PositionID || got.State != want.State || got.Score != want.Score {
		t.Errorf("request body = %+v, want %+v", got, want)
	}
	if len(got.Values) != 1 || got.Values[0] != want.Values[0] {
		t.Errorf("request values = %+v, want %+v", got.Values, want.Values)
	}
	if out.Stored != 1 {
		t.Errorf("decoded = %+v, want stored=1", out)
	}
}

func TestDoJSONNilBodySendsNoContentType(t *testing.T) {
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte("null"))
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	if err := c.DoJSON(context.Background(), http.MethodPost, "/x", nil, nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if gotContentType != "" {
		t.Errorf("Content-Type = %q, want empty", gotContentType)
	}
}

func TestPushContestDataRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != contestScoresPath(7) {
			t.Errorf("path = %q, want %s", r.URL.Path, contestScoresPath(7))
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var placements []ContestPlacement
		if err := json.NewDecoder(r.Body).Decode(&placements); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if len(placements) != 1 || placements[0].TeamName != "Team 1" {
			t.Errorf("placements = %+v, want one Team 1", placements)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"stored": 1})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	err := c.PushContestData(context.Background(), 7, []ContestPlacement{{TeamName: "Team 1", Score: 42}})
	if err != nil {
		t.Fatalf("PushContestData: %v", err)
	}
}

func TestGetContestsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != contestContestsPath {
			t.Errorf("path = %q, want %s", r.URL.Path, contestContestsPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]ContestInfo{
			{ID: 3, Name: "Winter FFA", Region: "North"},
		})
	}))
	defer server.Close()

	c := New(WithBaseURL(server.URL))
	contests, err := c.GetContests(context.Background())
	if err != nil {
		t.Fatalf("GetContests: %v", err)
	}
	if len(contests) != 1 {
		t.Fatalf("len(contests) = %d, want 1", len(contests))
	}
	want := ContestInfo{ID: 3, Name: "Winter FFA", Region: "North"}
	if contests[0] != want {
		t.Errorf("contests[0] = %+v, want %+v", contests[0], want)
	}
}
