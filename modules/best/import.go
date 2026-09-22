package best

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules/team"
)

// upsertTeamScore inserts or replaces the score value for a (team, type) pair.
func upsertTeamScore(ctx context.Context, m *Module, teamID, scoreTypeID uint, value float32) error {
	return gorm.G[TeamScoreValue](m.db.DB, clause.OnConflict{
		Columns:   []clause.Column{{Name: "team_id"}, {Name: "score_type_id"}},
		UpdateAll: true,
	}).Create(ctx, &TeamScoreValue{
		TeamID:      teamID,
		ScoreTypeID: scoreTypeID,
		Value:       value,
		SetAt:       time.Now(),
	})
}

func importExample(types []ScoreType) (header, row string) {
	headerCols := []string{"Team"}
	rowCols := []string{"42"}
	for _, st := range types {
		headerCols = append(headerCols, st.Name)
		rowCols = append(rowCols, strconv.FormatFloat(float64(st.Max)/4, 'f', 2, 32))
	}
	return strings.Join(headerCols, ","), strings.Join(rowCols, ",")
}

func (m *Module) uiViewImportScores(w http.ResponseWriter, r *http.Request) {
	types, err := gorm.G[ScoreType](m.db.DB).Order("`order` ASC").Find(r.Context())
	if err != nil {
		m.internalError(w, r, err)
		return
	}

	exampleHeader, exampleRow := importExample(types)
	ctx := pongo2.Context{
		"score_types":    types,
		"example_header": exampleHeader,
		"example_row":    exampleRow,
		"imported":       r.URL.Query().Get("imported"),
	}
	m.ws.DoTemplate(w, r, "views/best/import.p2", ctx)
}

func (m *Module) uiViewImportScoresSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	f, _, err := r.FormFile("scores_file")
	if err != nil {
		m.internalError(w, r, err)
		return
	}
	defer f.Close()

	types, err := gorm.G[ScoreType](m.db.DB).Order("`order` ASC").Find(ctx)
	if err != nil {
		m.internalError(w, r, err)
		return
	}

	teams, err := gorm.G[team.Team](m.db.DB).Find(ctx)
	if err != nil {
		m.internalError(w, r, err)
		return
	}
	teamByNumber := make(map[int]team.Team, len(teams))
	teamByName := make(map[string]team.Team, len(teams))
	for _, tm := range teams {
		teamByNumber[tm.Number] = tm
		teamByName[tm.Name] = tm
	}

	rd := csv.NewReader(f)
	var header []string
	rowNum := 0
	applied := 0
	var rowErrors []string

	for {
		record, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("Error decoding CSV", "error", err)
			m.internalError(w, r, fmt.Errorf("malformed CSV: %w", err))
			return
		}
		rowNum++
		if header == nil {
			for i := range record {
				record[i] = strings.TrimSpace(record[i])
			}
			header = record
			continue
		}

		row := make(map[string]string, len(header))
		for i, col := range header {
			if i < len(record) {
				row[col] = strings.TrimSpace(record[i])
			}
		}

		tm, ok := teamByName[row["Team"]]
		if n, err := strconv.Atoi(row["Team"]); err == nil {
			tm, ok = teamByNumber[n]
		}
		if !ok {
			rowErrors = append(rowErrors, fmt.Sprintf("Row %d: unknown team %q", rowNum, row["Team"]))
			continue
		}

		for _, st := range types {
			raw, present := row[st.Name]
			if !present || raw == "" {
				continue
			}
			val, err := strconv.ParseFloat(raw, 32)
			if err != nil {
				rowErrors = append(rowErrors, fmt.Sprintf("Row %d (%s): invalid %s score %q", rowNum, row["Team"], st.Name, raw))
				continue
			}
			if val < 0 || val > float64(st.Max) {
				rowErrors = append(rowErrors, fmt.Sprintf("Row %d (%s): %s score %s is out of range [0, %g]", rowNum, row["Team"], st.Name, raw, st.Max))
				continue
			}
			if err := upsertTeamScore(ctx, m, tm.ID, st.ID, float32(val)); err != nil {
				m.internalError(w, r, err)
				return
			}
			applied++
		}
	}

	if header == nil {
		m.internalError(w, r, fmt.Errorf("import file is missing a header row"))
		return
	}
	if !slices.Contains(header, "Team") {
		m.internalError(w, r, fmt.Errorf("import file must have a \"Team\" column"))
		return
	}

	if len(rowErrors) > 0 {
		ctx := pongo2.Context{
			"score_types": types,
			"row_errors":  rowErrors,
		}
		m.ws.DoTemplate(w, r, "views/best/import.p2", ctx)
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "scores", "import")+fmt.Sprintf("?imported=%d", applied), http.StatusSeeOther)
}
