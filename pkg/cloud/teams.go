package cloud

import (
	"context"
	"net/http"
	"net/url"
)

const (
	teamRegionsPath = "/ui/mod/team/cloud/regions"
	teamTeamsPath   = "/ui/mod/team/cloud/teams"
)

// RegionInfo describes a cloud region and how many teams it holds.
type RegionInfo struct {
	Name      string `json:"name"`
	TeamCount int    `json:"team_count"`
}

// TeamInfo is a single team roster entry from the cloud. Fields are
// names and numbers, never local database IDs, so they remain valid
// across systems with independent auto-increments.
type TeamInfo struct {
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Division string `json:"division"`
	Region   string `json:"region"`
}

// GetRegions lists every cloud region with its team count, ordered
// by region name.
func (c *Client) GetRegions(ctx context.Context) ([]RegionInfo, error) {
	var out []RegionInfo
	if err := c.Do(ctx, http.MethodGet, teamRegionsPath, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetRegionTeams lists the teams of the named region, ordered by
// team number. An empty region returns all teams.
func (c *Client) GetRegionTeams(ctx context.Context, region string) ([]TeamInfo, error) {
	query := url.Values{}
	if region != "" {
		query.Set("region", region)
	}
	var out []TeamInfo
	if err := c.Do(ctx, http.MethodGet, teamTeamsPath, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}
