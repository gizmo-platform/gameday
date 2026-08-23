package game

import (
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
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	c := Config{}
	if err := yaml.Unmarshal(b, &c); err != nil {
		slog.Error("Error while unmarshaling game config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	for _, pos := range c.Field.Positions {
		if err := db.InsertOrUpdate[FieldPosition](r.Context(), m.db.DB, &pos); err != nil {
			slog.Error("Error saving position", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
	}

	advFilterID := uint(1)
	for _, phase := range c.Game.Phases {
		for _, filter := range phase.AdvancementFilters {
			filter.GamePhaseID = phase.ID
			filter.ID = advFilterID
			slog.Debug("Loading filter", "id", advFilterID, "filter", filter)
			if err := db.InsertOrUpdate[GamePhaseAdvancementFilter](r.Context(), m.db.DB, &filter); err != nil {
				slog.Error("Error saving phase advancement", "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}
			advFilterID++
		}
		phase.AdvancementFilters = nil
		if err := db.InsertOrUpdate[GamePhase](r.Context(), m.db.DB, &phase); err != nil {
			slog.Error("Error saving phases", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
	}

	sceID := uint(1)
	for _, element := range c.Game.Elements {
		if err := db.InsertOrUpdate[GameElement](r.Context(), m.db.DB, &element); err != nil {
			slog.Error("Error saving element", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}

		for _, state := range element.States {
			state.GameElementID = element.ID
			if err := db.InsertOrUpdate[GameElementState](r.Context(), m.db.DB, &state); err != nil {
				slog.Error("Error saving element state", "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}

			err := db.InsertOrUpdate[ScorecardElement](r.Context(), m.db.DB, &ScorecardElement{
				ID:      sceID,
				Element: element.EID + "_" + state.SID,
				Type:    element.Type,
				Expr:    exprForElementState(element, state),
			})
			if err != nil {
				slog.Error("Error saving element state", "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}
			sceID++

			for _, value := range state.Values {
				value.GameElementStateID = state.ID
				if err := db.InsertOrUpdate[GameElementStateValue](r.Context(), m.db.DB, &value); err != nil {
					slog.Error("Error saving state value", "error", err)
					m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
					return
				}
			}
		}
	}

	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
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
