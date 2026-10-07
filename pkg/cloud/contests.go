package cloud

import (
	"context"
	"fmt"
	"net/http"
)

const contestContestsPath = "/ui/mod/contest/cloud/contests"

// ContestInfo is a single contest from the cloud. Unlike TeamInfo it
// deliberately carries the cloud database ID: an onsite server adopts
// the cloud contest ID as its own local contest ID, so the two
// systems reference the same contest by the same ID.
type ContestInfo struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

// GetContests lists every cloud contest, ordered by name.
func (c *Client) GetContests(ctx context.Context) ([]ContestInfo, error) {
	var out []ContestInfo
	if err := c.Do(ctx, http.MethodGet, contestContestsPath, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetContestRoster lists the teams on a single cloud contest's roster,
// ordered by team number.
func (c *Client) GetContestRoster(ctx context.Context, id uint) ([]TeamInfo, error) {
	path := fmt.Sprintf("/ui/mod/contest/cloud/contests/%d/roster", id)
	var out []TeamInfo
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
