package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"msb-manager/internal/msb"
	"msb-manager/internal/spec"
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

// maxSpecBytes caps the request body. A spec is small declarative YAML/JSON;
// anything larger than this is either a bug or an attack.
const maxSpecBytes = 64 * 1024

func handleCreateSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSpecBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read body"})
			return
		}
		if len(body) > maxSpecBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "spec too large"})
			return
		}
		s, err := spec.Parse(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := client.Create(r.Context(), s.ToCreateOpts()); err != nil {
			writeAdapterError(w, r, "create sandbox", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": s.Name, "image": s.Image})
	}
}

func handleStartSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Start(r.Context(), r.PathValue("name")); err != nil {
			writeAdapterError(w, r, "start sandbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleStopSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Stop(r.Context(), r.PathValue("name")); err != nil {
			writeAdapterError(w, r, "stop sandbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Rm(r.Context(), r.PathValue("name")); err != nil {
			writeAdapterError(w, r, "remove sandbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
