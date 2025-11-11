package game

import (
	"embed"
	"io/fs"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	CandidatePhase = 99
)

//go:embed ui/*
var efs embed.FS

// Field represents a field of play, and has at least one position on
// which a team can be placed.
type Config struct {
	Field  ConfigField
	Phases []GamePhase
}

type ConfigField struct {
	Positions []FieldPosition
}

// A FieldPosition has a name and a pair of colors, one of which
// should be lighter than the other to allow banding in tables.
type FieldPosition struct {
	ID     uint
	Name   string
	Color1 string
	Color2 string
}

// Field represents a single field that is available for scheduling.
type Field struct {
	ID   uint
	Name string
}

// GamePhase is a single phase of play that is part of a larger
// schedule.
type GamePhase struct {
	ID             uint
	Name           string
	AdvanceFrom    string
	AdvanceCount   int
	DivisionAware  bool
	ScheduleType   string
	ScoreSummation string
}

// MatchPlacement binds a team to a given field placement.
type MatchPlacement struct {
	ID uint

	Round   int
	Match   int
	Phase   GamePhase
	PhaseID uint

	Team       team.Team
	TeamID     uint
	Field      Field
	FieldID    uint
	Position   FieldPosition
	PositionID uint
}

type TeamModule interface {
	ListTeams(team.Team) ([]team.Team, error)
}

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	tm TeamModule

	basePath string
}

// Option allows for dynamic option passing to the module.
type Option func(*Module)

func New(opts ...Option) *Module {
	m := Module{
		r: chi.NewRouter(),
	}

	for _, o := range opts {
		o(&m)
	}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.uiViewGame)
		r.Get("/setup", m.uiViewSetupForm)
		r.Post("/setup", m.uiViewSetupSubmit)

		r.Route("/fields", func(r chi.Router) {
			r.Get("/", m.uiViewFieldList)
			r.Get("/add", m.uiViewFieldForm)
			r.Post("/add", m.uiViewFieldSubmit)

			r.Get("/{id}/edit", m.uiViewFieldForm)
			r.Post("/{id}/edit", m.uiViewFieldSubmit)
		})

		r.Route("/schedule", func(r chi.Router) {
			r.Get("/", m.uiViewPhaseList)
			r.Get("/phases/{id}", m.uiViewPhaseSchedule)
			r.Get("/phases/{id}/select-teams", m.uiViewPhaseScheduleSelectTeams)
			r.Post("/phases/{id}/select-teams", m.uiViewPhaseSchedulePreview)
			r.Post("/phases/{id}/accept-schedule", m.uiViewPhaseScheduleAccept)
		})
	})

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	return m.db.AutoMigrate(
		Field{},
		FieldPosition{},
		GamePhase{},
		MatchPlacement{},
	)
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	return []web.NavElement{{
		Text: "Game",
		Children: []web.NavChild{{
			Text:   "Configuration",
			Target: prefix,
		}, {
			Text:   "Setup",
			Target: path.Join(prefix, "/setup"),
		}, {
			Text:   "Fields",
			Target: path.Join(prefix, "/fields/"),
		}, {
			Text:   "Schedule",
			Target: path.Join(prefix, "/schedule/"),
		}},
	}}
}
