package game

import (
	"context"
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

// MatchState is an enum of states that a match can be in, used for
// filtering.
type MatchState uint

const (
	_ = iota
	// MatchStateScheduled refers to a match that exists in the
	// schedule, and can be played.
	MatchStateScheduled MatchState = iota

	// MatchStateQueued is used when a team is present in queuing
	// and is ready to play.
	MatchStateQueued

	// MatchStateRunning is used to identify when the clock is
	// running on a given match, and is primarily used to drive
	// external automations.
	MatchStateRunning

	// MatchStateScorable is defined as a match that is able to be
	// scored and is not in an otherwise terminal state.
	MatchStateScorable

	// MatchStateComplete is used to identify a match that is done
	// and does not require further scorecard manipulation.
	MatchStateComplete

	// MatchStateNoShow is used to identify a match placement
	// where the team never made it to the match, and forces a
	// zero score.
	MatchStateNoShow

	// MatchStateDisqualified is used to identify a team that may
	// have made it to a match, potentially was scored, but was
	// then disqualified from earning any points during the match
	// in question.
	MatchStateDisqualified
)

var (
	MatchStates = []MatchState{
		MatchStateScheduled,
		MatchStateQueued,
		MatchStateRunning,
		MatchStateScorable,
		MatchStateComplete,
		MatchStateNoShow,
		MatchStateDisqualified,
	}
)

func (m MatchState) String() string {
	switch m {
	case MatchStateScheduled:
		return "scheduled"
	case MatchStateQueued:
		return "queued"
	case MatchStateRunning:
		return "running"
	case MatchStateScorable:
		return "scorable"
	case MatchStateComplete:
		return "complete"
	case MatchStateNoShow:
		return "no-show"
	case MatchStateDisqualified:
		return "disqualified"
	default:
		return "unknown"
	}
}

//go:embed ui/*
var efs embed.FS

// Field represents a field of play, and has at least one position on
// which a team can be placed.
type Config struct {
	Field ConfigField
	Game  Game
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

// Game defines the components related to a playable game.
type Game struct {
	Phases   []GamePhase
	Elements []GameElement
}

// GamePhase is a single phase of play that is part of a larger
// schedule.
type GamePhase struct {
	ID             uint
	Name           string
	Active         bool
	AdvanceFrom    string
	AdvanceCount   int
	DivisionAware  bool
	ScheduleType   string
	ScoreSummation string
}

// GameElement represents a single game element that may be
// manipulated.
type GameElement struct {
	ID uint

	EID    string
	Name   string
	Desc   string
	Type   string
	States []GameElementState
}

// GameElementState represents a state that the game element may be
// in.
type GameElementState struct {
	ID            uint
	GameElementID uint

	SID  string
	Name string
	Desc string
	Each int
	Max  int

	Values []GameElementStateValue
}

// GameElementStateValue is a selectable value for game element states
// that do not use a linear scoring configuration.
type GameElementStateValue struct {
	ID                 uint
	GameElementStateID uint

	Name    string
	VID     string
	Points  int
	Default bool
}

// ScorecardElement is a single line from a scorecard that uses the ID
// format from the scorecards.
type ScorecardElement struct {
	ID uint

	Element string
	Type    string
	Expr    string
}

// ScorecardValue is a single attribute from a scorecard that has been scored.
type ScorecardValue struct {
	ID uint

	MatchPlacement   MatchPlacement
	MatchPlacementID uint

	Element string
	Value   int
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

	State MatchState
}

// MatchScore is used to store the actual numeric score as finalized
// from a MatchPlacement and collection of ScorecardValue elements.
type MatchScore struct {
	ID uint

	Score            int
	MatchPlacement   MatchPlacement
	MatchPlacementID uint

	GamePhase   GamePhase
	GamePhaseID uint

	Team   team.Team
	TeamID uint
}

type TeamModule interface {
	ListTeams(context.Context, team.Team) ([]team.Team, error)
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

	pongo2.RegisterFilter("key", filterGetValueByKey)

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
			r.Post("/phases/{id}/make-active", m.uiViewPhaseMakeActive)
			r.Get("/phases/{id}/select-teams", m.uiViewPhaseScheduleSelectTeams)
			r.Post("/phases/{id}/select-teams", m.uiViewPhaseSchedulePreview)
			r.Post("/phases/{id}/accept-schedule", m.uiViewPhaseScheduleAccept)
		})

		r.Route("/scorecard", func(r chi.Router) {
			r.Get("/", m.uiViewScorecardList)
			r.Get("/{phase}/{match}/{field}/{position}", m.uiViewScorecard)
			r.Post("/{phase}/{match}/{field}/{position}", m.uiViewScorecardSubmit)
		})
	})

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	return m.db.AutoMigrate(
		FieldPosition{},
		Field{},
		GameElementStateValue{},
		GameElementState{},
		GameElement{},
		GamePhase{},
		MatchPlacement{},
		MatchScore{},
		ScorecardElement{},
		ScorecardValue{},
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
			Target: path.Join(prefix, "/fields"),
		}, {
			Text:   "Schedule",
			Target: path.Join(prefix, "/schedule"),
		}, {
			Text:   "Scorecards",
			Target: path.Join(prefix, "/scorecard"),
		}},
	}}
}
