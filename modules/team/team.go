package team

import (
	"context"
	"embed"
	"encoding/csv"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	ModuleName = "TEAM"

	PermissionAdmin = "ADMIN"
)

//go:embed ui/*
var efs embed.FS

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	basePath string
}

type Team struct {
	ID uint

	Name     string
	Number   int
	Division string
	Region   string
}

// Option passes in multiple components to the module.
type Option func(m *Module)

func New(opts ...Option) *Module {
	m := Module{
		r: chi.NewRouter(),
	}

	for _, o := range opts {
		o(&m)
	}

	if err := m.ws.InstallPermission(context.Background(), ModuleName, PermissionAdmin); err != nil {
		return nil
	}

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.uiViewListTeams)
		r.Get("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportTeams))
		r.Post("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportTeamsSubmit))
		r.Get("/add", m.ws.GuardRoute(pAdmin, m.uiViewAddForm))
		r.Post("/add", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))

		r.Get("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewEditForm))
		r.Post("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))
	})

	pongo2.RegisterFilter("teamList", filterTeamList)

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	return m.db.AutoMigrate(Team{})
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix
	return []web.NavElement{{
		Text:   "Team",
		Weight: 80,
		Children: []web.NavChild{{
			Text:   "List",
			Target: path.Join(prefix, "/"),
		}, {
			Text:       "Bulk Import",
			Target:     path.Join(prefix, "/import"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}},
	}}
}

// ListTeams returns a selection of teams matching the filter.
func (m *Module) ListTeams(ctx context.Context, filter Team) ([]Team, error) {
	out, err := gorm.G[Team](m.db.DB).Where(filter).Find(ctx)
	return out, err
}

func filterTeamList(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	v, ok := in.Interface().([]Team)
	if !ok {
		return nil, &pongo2.Error{Sender: "teamList", OrigError: errors.New("team list was not a list")}
	}
	return pongo2.AsValue(v[param.Integer()].Name), nil
}

func (m *Module) uiViewListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := m.ListTeams(r.Context(), Team{})
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.ws.DoTemplate(w, r, "views/team/list.p2", pongo2.Context{"teams": teams})
}

func (m *Module) uiViewImportTeams(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/team/form_bulk.p2", nil)
}

func (m *Module) uiViewImportTeamsSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f, _, err := r.FormFile("teams_file")
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	defer f.Close()
	rd := csv.NewReader(f)
	teams := []map[string]string{}
	var header []string
	for {
		record, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("Error decoding CSV", "error", err)
			continue
		}
		if header == nil {
			header = record
			for col := range header {
				header[col] = strings.ReplaceAll(header[col], "Team Name", "Name")
				header[col] = strings.ReplaceAll(header[col], "Team Number", "Number")
				header[col] = strings.ReplaceAll(header[col], "Hub Name", "Region")
			}
		} else {
			dict := map[string]string{}
			for i := range header {
				dict[header[i]] = record[i]
			}
			if dict["Division"] == "" {
				dict["Division"] = "Open"
			}
			teams = append(teams, dict)
		}
	}

	for _, team := range teams {
		n, _ := strconv.Atoi(team["Number"])
		db.InsertOrUpdate[Team](r.Context(), m.db.DB, &Team{Name: team["Name"], Number: n, Division: team["Division"], Region: team["Region"]})
	}
	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

func (m *Module) uiViewAddForm(w http.ResponseWriter, r *http.Request) {
	divisions := []string{}
	if res := m.db.Model(&Team{}).Distinct("division").Find(&divisions); res.Error != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	regions := []string{}
	if res := m.db.Model(&Team{}).Distinct("region").Find(&regions); res.Error != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	if len(divisions) == 0 {
		divisions = append(divisions, "Open")
	}

	ctx := pongo2.Context{
		"divisions": divisions,
		"regions":   append(regions, "None"),
	}

	m.ws.DoTemplate(w, r, "views/team/form_single.p2", ctx)
}

func (m *Module) uiViewEditForm(w http.ResponseWriter, r *http.Request) {
	t, err := gorm.G[Team](m.db.DB).
		Where(&Team{ID: m.ws.StrToUint(chi.URLParam(r, "id"))}).
		First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	divisions := []string{}
	if res := m.db.Model(&Team{}).Distinct("division").Find(&divisions); res.Error != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	regions := []string{}
	if res := m.db.Model(&Team{}).Distinct("region").Find(&regions); res.Error != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	ctx := pongo2.Context{
		"team":      t,
		"divisions": divisions,
		"regions":   regions,
	}

	m.ws.DoTemplate(w, r, "views/team/form_single.p2", ctx)
}

func (m *Module) uiViewUpsert(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	t := Team{
		ID:       m.ws.StrToUint(chi.URLParam(r, "id")),
		Name:     r.FormValue("team_name"),
		Number:   int(m.ws.StrToUint(r.FormValue("team_number"))),
		Division: r.FormValue("team_division"),
		Region:   r.FormValue("team_region"),
	}

	if err := db.InsertOrUpdate[Team](r.Context(), m.db.DB, &t); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}
