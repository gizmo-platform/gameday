package team

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// TeamRow is a single roster entry pending upsert. Fields are names
// and numbers, never database IDs, so rows are portable across
// systems with independent auto-increments.
type TeamRow struct {
	Name     string
	Number   int
	Division string
	Region   string
}

// upsertTeamRows persists a set of roster rows: it gets or creates the
// divisions and regions the rows reference, then upserts each team
// keyed on team number. A re-import updates existing rows in place
// instead of duplicating them. Disabled is deliberately absent from
// the update set so a re-import cannot resurrect a manually-disabled
// team. It returns the number of rows applied; per-row upsert failures
// are logged and skipped, while division/region lookup or create
// failures are returned.
func (m *Module) upsertTeamRows(ctx context.Context, rows []TeamRow) (int, error) {
	// Get or create all divisions referenced by the rows
	divisionMap := make(map[string]uint)
	for _, row := range rows {
		if _, ok := divisionMap[row.Division]; ok {
			continue
		}

		div, err := gorm.G[Division](m.db.DB).Where(&Division{Name: row.Division}).First(ctx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			div = Division{Name: row.Division}
			if err := m.db.Create(&div).Error; err != nil {
				return 0, err
			}
		} else if err != nil {
			return 0, err
		}
		divisionMap[row.Division] = div.ID
	}

	// Get or create all regions referenced by the rows. A blank
	// region means the team belongs to no region.
	regionMap := make(map[string]uint)
	regionMap[""] = 0
	for _, row := range rows {
		if _, ok := regionMap[row.Region]; ok {
			continue
		}

		reg, err := gorm.G[Region](m.db.DB).Where(&Region{Name: row.Region}).First(ctx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			reg = Region{Name: row.Region}
			if err := m.db.Create(&reg).Error; err != nil {
				return 0, err
			}
		} else if err != nil {
			return 0, err
		}
		regionMap[row.Region] = reg.ID
	}

	applied := 0
	for _, row := range rows {
		err := db.InsertOrUpdate(ctx, m.db.DB, &Team{
			Name:       row.Name,
			Number:     row.Number,
			DivisionID: divisionMap[row.Division],
			RegionID:   regionMap[row.Region],
		}, clause.OnConflict{
			Columns:   []clause.Column{{Name: "number"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "division_id", "region_id"}),
		})
		if err != nil {
			slog.Error("Error upserting team on import", "number", row.Number, "error", err)
			continue
		}
		applied++
	}
	return applied, nil
}

// ImportTeams persists a set of roster rows, the same upsert the cloud
// and CSV imports use. It returns the number of rows applied.
func (m *Module) ImportTeams(ctx context.Context, rows []TeamRow) (int, error) {
	return m.upsertTeamRows(ctx, rows)
}

// cloudRegionsData is the public JSON endpoint listing every region
// and how many teams it holds, ordered by region name. Counts include
// disabled teams: the roster as the cloud system holds it.
func (m *Module) cloudRegionsData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	regions, err := gorm.G[Region](m.db.DB).Order("name ASC").Find(ctx)
	if err != nil {
		slog.Error("Error listing regions for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	type count struct {
		RegionID uint `gorm:"column:region_id"`
		Cnt      int  `gorm:"column:cnt"`
	}
	var counts []count
	if err := m.db.Model(&Team{}).Select("region_id, count(*) as cnt").Group("region_id").Find(&counts).Error; err != nil {
		slog.Error("Error counting teams per region", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	countByID := make(map[uint]int, len(counts))
	for _, c := range counts {
		countByID[c.RegionID] = c.Cnt
	}

	out := make([]cloud.RegionInfo, 0, len(regions))
	for _, region := range regions {
		out = append(out, cloud.RegionInfo{Name: region.Name, TeamCount: countByID[region.ID]})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("Error encoding cloud regions", "error", err)
	}
}

// cloudTeamsData is the public JSON endpoint listing the teams of a
// region (all regions when the query is absent), ordered by team
// number. Disabled teams are included: the full roster.
func (m *Module) cloudTeamsData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	name := r.URL.Query().Get("region")
	if name != "" {
		reg, err := gorm.G[Region](m.db.DB).Where(&Region{Name: name}).First(ctx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "region not found"})
			return
		}
		if err != nil {
			slog.Error("Error looking up region for cloud export", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		teams, err := gorm.G[Team](m.db.DB).
			Where("region_id = ?", reg.ID).
			Preload("Division", nil).
			Preload("Region", nil).
			Order("number ASC").
			Find(ctx)
		if err != nil {
			slog.Error("Error listing teams for cloud export", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		m.writeCloudTeams(w, teams)
		return
	}

	teams, err := gorm.G[Team](m.db.DB).
		Preload("Division", nil).
		Preload("Region", nil).
		Order("number ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error listing teams for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	m.writeCloudTeams(w, teams)
}

func (m *Module) writeCloudTeams(w http.ResponseWriter, teams []Team) {
	out := make([]cloud.TeamInfo, 0, len(teams))
	for _, team := range teams {
		out = append(out, cloud.TeamInfo{
			Number:   team.Number,
			Name:     team.Name,
			Division: team.Division.Name,
			Region:   team.Region.Name,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("Error encoding cloud teams", "error", err)
	}
}

// uiViewImportCloud lists the cloud regions and their team counts so
// an operator can pull a full region's roster into the local system.
func (m *Module) uiViewImportCloud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	regions, err := m.cloudClient.GetRegions(ctx)
	if err != nil {
		// A cloud.Error carries an actionable message; anything else
		// is a transport problem, but both are surfaced the same way.
		slog.Error("Error fetching cloud regions", "error", err)
		m.ws.DoTemplate(w, r, "views/team/import_cloud.p2", pongo2.Context{
			"cloudBase": m.cloudClient.BaseURL(),
			"error":     err.Error(),
		})
		return
	}

	imported := m.ws.StrToUint(r.URL.Query().Get("imported"))
	m.ws.DoTemplate(w, r, "views/team/import_cloud.p2", pongo2.Context{
		"cloudBase": m.cloudClient.BaseURL(),
		"regions":   regions,
		"imported":  imported,
	})
}

// uiViewImportCloudSubmit pulls the named region's roster from the
// cloud instance and upserts it into the local system.
func (m *Module) uiViewImportCloudSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	region := r.Form.Get("region")

	infos, err := m.cloudClient.GetRegionTeams(r.Context(), region)
	if err != nil {
		slog.Error("Error fetching cloud region teams", "region", region, "error", err)
		m.ws.DoTemplate(w, r, "views/team/import_cloud.p2", pongo2.Context{
			"cloudBase": m.cloudClient.BaseURL(),
			"error":     err.Error(),
			"region":    region,
		})
		return
	}

	rows := make([]TeamRow, 0, len(infos))
	for _, info := range infos {
		if info.Division == "" {
			info.Division = "Open"
		}
		rows = append(rows, TeamRow{
			Name:     info.Name,
			Number:   info.Number,
			Division: info.Division,
			Region:   info.Region,
		})
	}

	applied, err := m.upsertTeamRows(r.Context(), rows)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "/import-cloud")+"?imported="+strconv.Itoa(applied), http.StatusSeeOther)
}
