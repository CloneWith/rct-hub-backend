package authsession

import (
	"net/http"
	"time"
)

// CookieConfig carries the browser cookie attributes used to refresh a sliding
// session cookie. TTL is the cookie Max-Age written on renewal; it should be at
// least the idle window so the browser keeps the secret around while the
// server-side session stays alive. The server-side absolute deadline remains
// the hard upper bound and forces re-login regardless of cookie freshness.
type CookieConfig struct {
	Name     string
	Domain   string
	Secure   bool
	SameSite http.SameSite
	TTL      time.Duration
}

// RefreshCookie writes a fresh Set-Cookie carrying the same secret so the
// browser's Max-Age restarts whenever the server-side sliding window is
// renewed. Without this, an always-active user would still be logged out when
// the original cookie expired, even though the Redis session was kept alive.
func RefreshCookie(w http.ResponseWriter, cfg CookieConfig, value string) {
	if w == nil || value == "" || cfg.Name == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.Name,
		Value:    value,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   int(cfg.TTL.Seconds()),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
	})
}
