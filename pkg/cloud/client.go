// Package cloud provides an HTTP client for talking to a Gameday
// cloud instance. It is the single place that owns the cloud base
// URL, the outbound http.Client, the authentication seam, and the
// per-feature wire contracts. Onsite components (and future cloud
// integrations) should construct a Client rather than issuing
// outbound requests directly.
package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is the canonical Gameday cloud instance. It is used
// when the GAMEDAY_CLOUD environment variable is not set.
const DefaultBaseURL = "https://gameday.gizmoplatform.org"

// defaultTimeout bounds every request made through the client.
const defaultTimeout = 10 * time.Second

// Error is returned when the cloud instance responds with a
// non-2xx status. Message carries the (trimmed, capped) response
// body so failures are actionable from the UI.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("cloud returned status %d: %s", e.Status, e.Message)
}

// NewErrorFromBody builds an Error from a non-2xx response body.
func NewErrorFromBody(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		body = []byte("unreadable response body")
	}
	return &Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

// Client is a thin HTTP client bound to one cloud instance.
type Client struct {
	base string
	http *http.Client
	auth Authenticator
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL pins the cloud base URL, bypassing GAMEDAY_CLOUD.
func WithBaseURL(base string) Option {
	return func(c *Client) {
		c.base = strings.TrimRight(base, "/")
	}
}

// WithTimeout overrides the per-request timeout (default 10s).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

// WithAuthenticator installs the credential provider applied to
// every outbound request.
func WithAuthenticator(a Authenticator) Option {
	return func(c *Client) {
		c.auth = a
	}
}

// WithBasicAuth installs an HTTP Basic authenticator, a convenience
// for WithAuthenticator(BasicAuth{...}).
func WithBasicAuth(username, password string) Option {
	return WithAuthenticator(BasicAuth{Username: username, Password: password})
}

// New creates a Client for the cloud instance named by the
// GAMEDAY_CLOUD environment variable, or DefaultBaseURL when unset.
// Options may override the base URL, timeout, and authenticator.
func New(opts ...Option) *Client {
	base := os.Getenv("GAMEDAY_CLOUD")
	if base == "" {
		base = DefaultBaseURL
	}
	c := &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the base URL the client targets, without a
// trailing slash.
func (c *Client) BaseURL() string {
	return c.base
}

// Do issues a request against the cloud instance and, when out is
// non-nil, decodes the JSON response body into it. Non-2xx statuses
// return a *Error; transport and context failures return the
// underlying error unchanged.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	if c.auth != nil {
		if err := c.auth.Apply(ctx, req); err != nil {
			return err
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return NewErrorFromBody(resp)
	}

	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
