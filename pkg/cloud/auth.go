package cloud

import (
	"context"
	"net/http"
)

// Authenticator decorates an outbound request with credentials
// before it is sent. It is the extension point for authenticating
// against cloud features that require it: install one with
// WithAuthenticator (or WithBasicAuth / WithBearerToken) at client
// construction and every request the client makes is signed for that
// provider.
type Authenticator interface {
	Apply(ctx context.Context, req *http.Request) error
}

// BasicAuth is an HTTP Basic authenticator.
type BasicAuth struct {
	Username string
	Password string
}

// Apply sets the Authorization header on req using HTTP Basic.
func (b BasicAuth) Apply(ctx context.Context, req *http.Request) error {
	req.SetBasicAuth(b.Username, b.Password)
	return nil
}

// BearerAuth is a bearer token authenticator.
type BearerAuth struct {
	Token string
}

// Apply sets the Authorization header on req using the bearer
// scheme.
func (b BearerAuth) Apply(ctx context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+b.Token)
	return nil
}
