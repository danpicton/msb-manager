package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// requireBearer guards next with the single bearer token. Both tokens are
// hashed and the fixed-length digests compared in constant time, so response
// timing is independent of how the presented token relates to the real one —
// including its length (ConstantTimeCompare alone short-circuits on length
// mismatch, issue #24). An empty configured token denies everything (the
// server should refuse to start without one, but we fail closed here
// regardless).
func requireBearer(token string, next http.Handler) http.Handler {
	wantDigest := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if len(token) == 0 || !strings.HasPrefix(h, bearerPrefix) {
			unauthorized(w)
			return
		}
		gotDigest := sha256.Sum256([]byte(strings.TrimPrefix(h, bearerPrefix)))
		if subtle.ConstantTimeCompare(gotDigest[:], wantDigest[:]) != 1 {
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
