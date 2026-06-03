package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"msb-manager/internal/msb"
)

func handleListSandboxes(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sandboxes, err := client.List(r.Context())
		if err != nil {
			writeAdapterError(w, r, "list sandboxes", err)
			return
		}
		// Non-nil empty slice serialises as [] not null.
		if sandboxes == nil {
			sandboxes = []msb.Sandbox{}
		}
		writeJSON(w, http.StatusOK, sandboxes)
	}
}

func handleInspectSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		detail, err := client.Inspect(r.Context(), name)
		if err != nil {
			writeAdapterError(w, r, "inspect sandbox", err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeAdapterError surfaces an adapter failure as a generic 500. msb's
// stderr/exit-code → finer-grained status mapping (404 on not-found, etc.) is
// the next refinement; right now every adapter error is opaque.
func writeAdapterError(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.ErrorContext(r.Context(), "adapter call failed", "op", op, "err", err.Error())
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": op + " failed"})
}
