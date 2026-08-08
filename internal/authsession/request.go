package authsession

import (
	"errors"
	"net/http"
	"strings"

	"rctHubBackend/pkg/jwtutil"
)

var ErrNoCredentials = errors.New("authentication credentials are missing")

// ClaimsFromRequest keeps script Bearer tokens and browser sessions as
// separate credentials while exposing the same claims to request handlers.
func ClaimsFromRequest(request *http.Request, signer *jwtutil.Signer, sessions Resolver, cookieName string) (*jwtutil.Claims, error) {
	header := request.Header.Get("Authorization")
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			return nil, errors.New("invalid authorization header")
		}
		return signer.Parse(parts[1])
	}
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, ErrNoCredentials
		}
		return nil, err
	}
	if sessions == nil {
		return nil, ErrNoCredentials
	}
	return sessions.Resolve(request.Context(), cookie.Value)
}
