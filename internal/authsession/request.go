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
// The boolean reports whether the underlying browser session was slid by this
// call (always false for Bearer tokens), so HTTP layers can refresh the cookie.
func ClaimsFromRequest(request *http.Request, signer *jwtutil.Signer, sessions Resolver, cookieName string) (*jwtutil.Claims, bool, error) {
	header := request.Header.Get("Authorization")
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			return nil, false, errors.New("invalid authorization header")
		}
		claims, err := signer.Parse(parts[1])
		return claims, false, err
	}
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, false, ErrNoCredentials
		}
		return nil, false, err
	}
	if sessions == nil {
		return nil, false, ErrNoCredentials
	}
	if renewer, ok := sessions.(RenewalResolver); ok {
		claims, renewed, err := renewer.ResolveWithRenewal(request.Context(), cookie.Value)
		return claims, renewed, err
	}
	claims, err := sessions.Resolve(request.Context(), cookie.Value)
	return claims, false, err
}
