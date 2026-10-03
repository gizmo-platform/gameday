package game

import (
	"log/slog"
	"net/http"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/goccy/go-yaml"
	"gorm.io/gorm"
)

// cloudSetupData is the public endpoint that serves the game setup
// as the canonical YAML document. It is public on purpose: the game
// file is game design, not sensitive data, and is intended to be
// pulled by other Gameday systems. Runtime state is excluded so a
// re-import represents the game design, not the serving system's
// live operations: phase Active/Frozen/HideScores flags are reset,
// and the clock's next-match pointer never leaves the system.
func (m *Module) cloudSetupData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	positions, err := gorm.G[FieldPosition](m.db.DB).Order("id ASC").Find(ctx)
	if err != nil {
		slog.Error("Error loading positions for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	phases, err := gorm.G[GamePhase](m.db.DB).
		Preload("AdvancementFilters", nil).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error loading phases for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error loading elements for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for i := range phases {
		phases[i].Active = false
		phases[i].Frozen = false
		phases[i].HideScores = false
	}

	c := Config{
		Field: ConfigField{
			Positions: positions,
		},
		Game: Game{
			Phases:   phases,
			Elements: elements,
		},
	}

	// Emit the clock section only when a clock is configured, so an
	// import into a system without a clock leaves its (absent) clock
	// untouched, matching the file-upload path.
	if cs, err := m.clockSettings(ctx); err == nil {
		c.Clock = ClockConfig{
			Duration:    cs.Duration,
			Hurry:       cs.Hurry,
			Transitions: cs.Transitions,
		}
	}

	b, err := yaml.Marshal(c)
	if err != nil {
		slog.Error("Error encoding cloud game setup", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/yaml")
	if _, err := w.Write(b); err != nil {
		slog.Error("Error writing cloud game setup", "error", err)
	}
}

// uiViewImportCloud renders the cloud import page. Fetching the file
// up front surfaces cloud failures on the page before the operator
// clicks import.
func (m *Module) uiViewImportCloud(w http.ResponseWriter, r *http.Request) {
	if _, err := m.cloudClient.GetGameFile(r.Context()); err != nil {
		slog.Error("Error fetching cloud game file", "error", err)
		m.ws.DoTemplate(w, r, "views/game/import_cloud.p2", pongo2.Context{
			"cloudBase": m.cloudClient.BaseURL(),
			"error":     err.Error(),
		})
		return
	}

	m.ws.DoTemplate(w, r, "views/game/import_cloud.p2", pongo2.Context{
		"cloudBase": m.cloudClient.BaseURL(),
		"imported":  m.ws.StrToUint(r.URL.Query().Get("imported")),
	})
}

// uiViewImportCloudSubmit pulls the game setup file from the cloud
// instance and applies it with the same upsert as a local file
// upload.
func (m *Module) uiViewImportCloudSubmit(w http.ResponseWriter, r *http.Request) {
	b, err := m.cloudClient.GetGameFile(r.Context())
	if err != nil {
		slog.Error("Error fetching cloud game file", "error", err)
		m.ws.DoTemplate(w, r, "views/game/import_cloud.p2", pongo2.Context{
			"cloudBase": m.cloudClient.BaseURL(),
			"error":     err.Error(),
		})
		return
	}

	c := Config{}
	if err := yaml.Unmarshal(b, &c); err != nil {
		slog.Error("Error while unmarshaling cloud game config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	if err := m.applyGameConfig(r.Context(), c); err != nil {
		slog.Error("Error applying cloud game config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "/import-cloud")+"?imported=1", http.StatusSeeOther)
}
