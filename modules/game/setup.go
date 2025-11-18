package game

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/goccy/go-yaml"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) uiViewGame(w http.ResponseWriter, r *http.Request) {
	c := Config{}

	if res := m.db.Find(&c.Field.Positions); res.Error != nil {
		slog.Error("Error retreiving field positions", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	if res := m.db.Find(&c.Game.Phases); res.Error != nil {
		slog.Error("Error retreiving game phases", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	if res := m.db.Preload(clause.Associations).Find(&c.Game.Elements); res.Error != nil {
		slog.Error("Error retreiving game elements", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
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
		if res := m.db.Save(&pos); res.Error != nil {
			slog.Error("Error saving position", "error", res.Error)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
			return
		}
	}

	for _, phase := range c.Game.Phases {
		if res := m.db.Save(&phase); res.Error != nil {
			slog.Error("Error saving phases", "error", res.Error)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
			return
		}
	}

	for _, element := range c.Game.Elements {
		if res := m.db.Save(&element); res.Error != nil {
			slog.Error("Error saving element", "error", res.Error)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
			return
		}

		for _, state := range element.States {
			state.GameElementID = element.ID
			if res := m.db.Save(&state); res.Error != nil {
				slog.Error("Error saving element state", "error", res.Error)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
				return
			}

			for _, value := range state.Values {
				value.GameElementStateID = state.ID
				if res := m.db.Save(&value); res.Error != nil {
					slog.Error("Error saving state value", "error", res.Error)
					m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
					return
				}
			}
		}
	}

	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

func (m *Module) uiViewFieldList(w http.ResponseWriter, r *http.Request) {
	fList := []Field{}

	if res := m.db.Find(&fList); res.Error != nil {
		slog.Error("Error loading fields", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	m.ws.DoTemplate(w, r, "views/game/fields.p2", pongo2.Context{"fields": fList})
}

func (m *Module) uiViewFieldForm(w http.ResponseWriter, r *http.Request) {
	fID := m.ws.StrToUint(chi.URLParam(r, "id"))

	field := Field{}
	if res := m.db.Where(&Field{ID: fID}).First(&field); res.Error != nil && !errors.Is(res.Error, gorm.ErrRecordNotFound) {
		slog.Error("Error loading fields", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}
	m.ws.DoTemplate(w, r, "views/game/field_form.p2", pongo2.Context{"field": field})
}

func (m *Module) uiViewFieldSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	fID := m.ws.StrToUint(chi.URLParam(r, "id"))

	if res := m.db.Save(&Field{ID: fID, Name: r.FormValue("field_name")}); res.Error != nil {
		slog.Error("Error saving position", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "fields/"), http.StatusSeeOther)
}
