package game

import (
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"
)

func (m *Module) uiViewCalculator(w http.ResponseWriter, r *http.Request) {
	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"Elements": elements,
		"IsCalculator": true,
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard.p2", ctx)
}
