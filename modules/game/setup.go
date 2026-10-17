package game

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/goccy/go-yaml"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

func (m *Module) uiViewGame(w http.ResponseWriter, r *http.Request) {
	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving game phases", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving game elements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
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

	m.ws.DoTemplate(w, r, "views/game/config.p2", pongo2.Context{"config": c})
}

func (m *Module) uiViewSetupForm(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/game/setup.p2", nil)
}

func (m *Module) uiViewSetupSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f, _, err := r.FormFile("game_config")
	if err != nil {
		slog.Error("Error while extracting file", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	c := Config{}
	if err := yaml.Unmarshal(b, &c); err != nil {
		slog.Error("Error while unmarshaling game config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if err := m.applyGameConfig(r.Context(), c); err != nil {
		slog.Error("Error applying game config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "/active-configuration"), http.StatusSeeOther)
}

// applyGameConfig persists a parsed game setup document: field
// positions, phases with their advancement filters, game elements
// with states, values, and scorecard expressions, and (when present)
// the clock section. It is the shared upsert used by both the local
// file upload and the cloud import, so the two paths behave
// identically.
func (m *Module) applyGameConfig(ctx context.Context, c Config) error {
	for _, pos := range c.Field.Positions {
		if err := db.InsertOrUpdate[FieldPosition](ctx, m.db.DB, &pos); err != nil {
			return err
		}
	}

	advFilterID := uint(1)
	for _, phase := range c.Game.Phases {
		filters := phase.AdvancementFilters
		phase.AdvancementFilters = nil
		// Active, Frozen, and HideScores are runtime operations state,
		// owned by the schedule page's make-active, make-frozen, and
		// hide-scores actions — not design state.  The cloud export
		// deliberately zeroes them, and a plain re-upload of any file
		// would otherwise silently unfreeze, deactivate, or unhide a
		// live event's phases, so a re-uploaded phase inherits the
		// stored flags.  A brand-new phase keeps the file's values.
		if phase.ID != 0 {
			cur, err := gorm.G[GamePhase](m.db.DB).
				Where(&GamePhase{ID: phase.ID}).
				First(ctx)
			switch {
			case err == nil:
				phase.Active = cur.Active
				phase.Frozen = cur.Frozen
				phase.HideScores = cur.HideScores
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return err
			}
		}
		// The phase must exist before its advancement filters: Postgres
		// enforces the game_phase_id foreign key, so inserting a filter
		// before its phase fails (SQLite does not enforce FKs, which is
		// why this ordering bug only surfaced on Postgres).
		if err := db.InsertOrUpdate[GamePhase](ctx, m.db.DB, &phase); err != nil {
			return err
		}
		for _, filter := range filters {
			filter.GamePhaseID = phase.ID
			filter.ID = advFilterID
			slog.Debug("Loading filter", "id", advFilterID, "filter", filter)
			if err := db.InsertOrUpdate[GamePhaseAdvancementFilter](ctx, m.db.DB, &filter); err != nil {
				return err
			}
			advFilterID++
		}
	}

	sceID := uint(1)
	for _, element := range c.Game.Elements {
		if err := db.InsertOrUpdate[GameElement](ctx, m.db.DB, &element); err != nil {
			return err
		}

		for _, state := range element.States {
			state.GameElementID = element.ID
			if err := db.InsertOrUpdate[GameElementState](ctx, m.db.DB, &state); err != nil {
				return err
			}

			if err := db.InsertOrUpdate[ScorecardElement](ctx, m.db.DB, &ScorecardElement{
				ID:      sceID,
				Element: element.EID + "_" + state.SID,
				Type:    element.Type,
				Expr:    exprForElementState(element, state),
			}); err != nil {
				return err
			}
			sceID++

			for _, value := range state.Values {
				value.GameElementStateID = state.ID
				if err := db.InsertOrUpdate[GameElementStateValue](ctx, m.db.DB, &value); err != nil {
					return err
				}
			}
		}
	}

	if c.Clock != (ClockConfig{}) {
		settings := ClockSettings{
			ID:          ClockSettingsID,
			Duration:    c.Clock.Duration,
			Hurry:       c.Clock.Hurry,
			Transitions: c.Clock.Transitions,
		}
		// A re-upload resets the file-sourced fields but preserves
		// the operator's next-match override, which is runtime state
		// set from the timekeeping page, not file state.
		if cur, err := m.clockSettings(ctx); err == nil {
			settings.NextMatch = cur.NextMatch
			settings.NextMatchPhase = cur.NextMatchPhase
		}
		if err := db.InsertOrUpdate[ClockSettings](ctx, m.db.DB, &settings); err != nil {
			return err
		}
	}

	return nil
}

func (m *Module) uiViewFieldList(w http.ResponseWriter, r *http.Request) {
	fList, err := m.ListFields(r.Context(), Field{})
	if err != nil {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.ws.DoTemplate(w, r, "views/game/fields.p2", pongo2.Context{"fields": fList})
}

func (m *Module) uiViewFieldForm(w http.ResponseWriter, r *http.Request) {
	fID := m.ws.StrToUint(chi.URLParam(r, "id"))

	fieldList, err := m.ListFields(r.Context(), Field{ID: fID})
	field := Field{}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if len(fieldList) > 0 {
		field = fieldList[0]
	}

	divisions, err := gorm.G[team.Division](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading divisions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.ws.DoTemplate(w, r, "views/game/field_form.p2", pongo2.Context{
		"field":     field,
		"divisions": divisions,
	})
}

func (m *Module) uiViewFieldSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	field := Field{
		ID:   m.ws.StrToUint(chi.URLParam(r, "id")),
		Name: r.FormValue("field_name"),
	}

	if err := db.InsertOrUpdate[Field](r.Context(), m.db.DB, &field); err != nil {
		slog.Error("Error saving field", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Update field-division associations
	divs := m.selectedDivisions(r)
	divsIface := make([]interface{}, len(divs))
	for i, d := range divs {
		divsIface[i] = d
	}
	if err := m.db.Raw().Model(&field).Association("Divisions").Replace(divsIface...); err != nil {
		slog.Error("Error updating field divisions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "fields/"), http.StatusSeeOther)
}

func (m *Module) selectedDivisions(r *http.Request) []team.Division {
	divisions := make([]team.Division, 0)
	for key, values := range r.PostForm {
		if len(values) > 0 && strings.HasPrefix(key, "division_") {
			id := m.ws.StrToUint(strings.TrimPrefix(key, "division_"))
			if id > 0 {
				divisions = append(divisions, team.Division{ID: id})
			}
		}
	}
	return divisions
}
