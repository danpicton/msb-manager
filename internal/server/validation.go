package server

import (
	"net/http"

	"msb-manager/internal/msb"
)

// validPathName checks a path-sourced identifier (a {name} segment) before it
// reaches the msb adapter. ServeMux percent-decodes path segments, so a request
// to /sandboxes/%2D%2Dforce arrives here as "--force" — which msb would parse as
// a flag (issue #3). On a malformed name it writes 400 and returns false so the
// caller can return early without invoking any subprocess.
func validPathName(w http.ResponseWriter, name string) bool {
	if !msb.ValidName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return false
	}
	return true
}
