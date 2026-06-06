package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// requireBearer guards next with the single bearer token. The comparison is
// constant-time so a wrong token leaks no timing signal. An empty configured
// token denies everything (the server should refuse to start without one, but
// we fail closed here regardless).
func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if len(token) == 0 || !strings.HasPrefix(h, bearerPrefix) {
			unauthorized(w)
			return
		}
		got := []byte(strings.TrimPrefix(h, bearerPrefix))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
}
