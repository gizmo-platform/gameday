package cloud

import (
	"context"
	"net/http"
	"net/url"
)

// gameSetupPath is the public endpoint on a cloud instance that
// serves the game setup file as raw YAML. The game file is game
// design (phases, elements, scoring, clock), not sensitive data, so
// the endpoint requires no authentication.
const gameSetupPath = "/ui/mod/game/cloud/setup"

// GetGameFile fetches the raw game setup file (YAML) from the cloud
// instance. The bytes are the canonical setup document: the importer
// on the other end parses it with the same code path as a local file
// upload, so the file stays the single source of truth.
func (c *Client) GetGameFile(ctx context.Context) ([]byte, error) {
	return c.DoBytes(ctx, http.MethodGet, gameSetupPath, url.Values{})
}
