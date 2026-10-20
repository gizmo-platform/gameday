package contest

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// OnsiteContest is the local record of which cloud contest this
// onsite server is handling. Its ID is the cloud contest ID: the
// onsite server adopts the cloud's auto-increment ID as its own local
// contest ID, so both systems reference the same contest by the same
// ID. Name and Region are stored as plain strings because the onsite
// database has no cloud region rows to link against.
type OnsiteContest struct {
	ID uint

	Name   string
	Region string
}

// TeamModule is the subset of the team module the onsite contest
// module needs: the roster emptiness check and the import upsert.
type TeamModule interface {
	ListTeams(ctx context.Context, filter team.Team) ([]team.Team, error)
	ImportTeams(ctx context.Context, rows []team.TeamRow) (int, error)
}

// GameModule is the subset of the game module the onsite contest
// module needs: pulling the cloud game file into the local database.
type GameModule interface {
	ImportGame(ctx context.Context) error
}

// OnsiteModule is the onsite component of the contest module. It
// exposes a "My Contest" page where the operator picks which cloud
// contest this onsite server is handling; the chosen cloud contest ID
// becomes the local contest ID.
type OnsiteModule struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	cloudClient *cloud.Client

	tm TeamModule
	gm GameModule

	basePath string
}

func NewOnsite(scope string, db *db.DB, ws *web.Server, deps modules.ModuleDeps) *OnsiteModule {
	m := OnsiteModule{
		r:  chi.NewRouter(),
		db: db,
		ws: ws,

		cloudClient: cloud.New(),
	}

	if tm, ok := deps["team"].(TeamModule); ok {
		m.tm = tm
	}
	if gm, ok := deps["game"].(GameModule); ok {
		m.gm = gm
	}

	if err := m.ws.InstallPermission(context.Background(), ModuleName, PermissionAdmin); err != nil {
		return nil
	}

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.ws.GuardRoute(pAdmin, m.uiViewMyContest))
		r.Post("/", m.ws.GuardRoute(pAdmin, m.uiViewSelectContest))
	})

	return &m
}

func (m *OnsiteModule) Router() chi.Router {
	return m.r
}

func (m *OnsiteModule) Migrate() error {
	return m.db.AutoMigrate(OnsiteContest{})
}

func (m *OnsiteModule) TemplateLoader() pongo2.TemplateLoader {
	if m.ws != nil && m.ws.TemplateDebug() {
		return web.DebugTemplateLoader("modules/contest/ui/p2", efs)
	}
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *OnsiteModule) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	children := []web.NavChild{{
		Text:       "My Contest",
		Target:     path.Join(prefix, "/"),
		Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
	}}

	return []web.NavElement{{
		Text:     "My Contest",
		Weight:   98,
		Children: children,
	}}
}

// currentContest loads the locally selected contest, or a zero value
// when none has been selected yet.
func (m *OnsiteModule) currentContest(ctx context.Context) OnsiteContest {
	c, err := gorm.G[OnsiteContest](m.db.DB).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OnsiteContest{}
	}
	if err != nil {
		slog.Error("Error loading selected contest", "error", err)
		return OnsiteContest{}
	}
	return c
}

// uiViewMyContest renders the My Contest page. Fetching the cloud
// contest list up front surfaces cloud failures on the page before
// the operator picks a contest.
func (m *OnsiteModule) uiViewMyContest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	contests, err := m.cloudClient.GetContests(ctx)
	if err != nil {
		slog.Error("Error fetching cloud contests", "error", err)
		m.ws.DoTemplate(w, r, "views/contest/my_contest.p2", pongo2.Context{
			"cloudBase":  m.cloudClient.BaseURL(),
			"moduleBase": m.basePath,
			"error":      err,
		})
		return
	}

	selected := m.currentContest(ctx)

	// The frozen flag lives on the cloud's contest, not the local
	// selection row, so read it from the freshly fetched list.
	selectedFrozen := false
	for i := range contests {
		if contests[i].ID == selected.ID {
			selectedFrozen = contests[i].Frozen
			break
		}
	}

	m.ws.DoTemplate(w, r, "views/contest/my_contest.p2", pongo2.Context{
		"cloudBase":      m.cloudClient.BaseURL(),
		"moduleBase":     m.basePath,
		"contests":       contests,
		"selected":       selected,
		"selectedFrozen": selectedFrozen,
		"imported":       m.ws.StrToUint(r.URL.Query().Get("imported")),
	})
}

// uiViewSelectContest persists the chosen cloud contest as the local
// contest: the stored row's ID is the cloud contest ID.
func (m *OnsiteModule) uiViewSelectContest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := m.ws.StrToUint(r.FormValue("contest_id"))
	if id == 0 {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": errors.New("no contest selected")})
		return
	}

	var info *cloud.ContestInfo
	contests, err := m.cloudClient.GetContests(ctx)
	if err != nil {
		slog.Error("Error fetching cloud contests", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	for i := range contests {
		if contests[i].ID == id {
			info = &contests[i]
			break
		}
	}
	if info == nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": errors.New("unknown contest")})
		return
	}

	// Replace any previous selection so the local row's ID is the
	// chosen cloud contest ID.
	if err := m.db.Where("1 = 1").Delete(&OnsiteContest{}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if err := m.db.Create(&OnsiteContest{ID: id, Name: info.Name, Region: info.Region}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.importContestData(ctx, id)

	http.Redirect(w, r, m.basePath+"?imported=1", http.StatusSeeOther)
}

// importContestData seeds the local system from the cloud after a
// contest is selected: the contest's roster is imported when the local
// team list is empty, and the cloud game is imported when no game is
// loaded. Both are best-effort: a transient cloud failure is logged
// and never blocks the selection itself.
func (m *OnsiteModule) importContestData(ctx context.Context, id uint) {
	if m.tm != nil {
		teams, err := m.tm.ListTeams(ctx, team.Team{})
		if err != nil {
			slog.Error("Error listing local teams for contest import", "error", err)
		} else if len(teams) == 0 {
			m.importContestRoster(ctx, id)
		}
	}

	if m.gm != nil {
		var count int64
		if err := m.db.Model(&game.GamePhase{}).Count(&count).Error; err != nil {
			slog.Error("Error counting game phases for contest import", "error", err)
		} else if count == 0 {
			if err := m.gm.ImportGame(ctx); err != nil {
				slog.Error("Error importing cloud game", "error", err)
			}
		}
	}
}

// importContestRoster pulls the selected contest's roster from the
// cloud and upserts it into the local team table.
func (m *OnsiteModule) importContestRoster(ctx context.Context, id uint) {
	roster, err := m.cloudClient.GetContestRoster(ctx, id)
	if err != nil {
		slog.Error("Error fetching cloud contest roster", "contest", id, "error", err)
		return
	}

	rows := make([]team.TeamRow, 0, len(roster))
	for _, t := range roster {
		division := t.Division
		if division == "" {
			division = "Open"
		}
		rows = append(rows, team.TeamRow{
			Name:     t.Name,
			Number:   t.Number,
			Division: division,
			Region:   t.Region,
		})
	}

	if _, err := m.tm.ImportTeams(ctx, rows); err != nil {
		slog.Error("Error importing contest roster", "contest", id, "error", err)
	}
}
