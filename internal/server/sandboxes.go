package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"msb-manager/internal/lock"
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

func handleCreateSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
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

		// Claim any named volume before touching msb. On adapter failure we
		// roll back so the volume isn't held by a sandbox that doesn't exist.
		volumes := volumesFromSpec(s)
		if err := vlock.AcquireMany(volumes, s.Name); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err := client.Create(r.Context(), s.ToCreateOpts()); err != nil {
			vlock.Release(s.Name)
			writeAdapterError(w, r, "create sandbox", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": s.Name, "image": s.Image})
	}
}

func handleStartSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		// Look up the sandbox's volumes so we can claim them. A Start of a
		// missing sandbox surfaces as msb.ErrSandboxNotFound -> 404.
		detail, err := client.Inspect(r.Context(), name)
		if err != nil {
			writeAdapterError(w, r, "start sandbox", err)
			return
		}
		volumes := detail.VolumeNames()
		if err := vlock.AcquireMany(volumes, name); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err := client.Start(r.Context(), name); err != nil {
			vlock.Release(name)
			writeAdapterError(w, r, "start sandbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleStopSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := client.Stop(r.Context(), name); err != nil {
			writeAdapterError(w, r, "stop sandbox", err)
			return
		}
		// Volume claim is tied to the sandbox being *running*; a stop frees it.
		vlock.Release(name)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := client.Rm(r.Context(), name); err != nil {
			writeAdapterError(w, r, "remove sandbox", err)
			return
		}
		// Release any claim (no-op if the sandbox was already stopped).
		vlock.Release(name)
		w.WriteHeader(http.StatusNoContent)
	}
}

func volumesFromSpec(s spec.Spec) []string {
	if s.Volume == nil {
		return nil
	}
	return []string{s.Volume.Name}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeAdapterError surfaces an adapter failure as an HTTP response, mapping
// recognised msb sentinels to specific statuses. Anything we don't recognise
// stays 500 — surfacing arbitrary stderr text to clients would be a leak.
func writeAdapterError(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.ErrorContext(r.Context(), "adapter call failed", "op", op, "err", err.Error())
	status, msg := statusForAdapterError(op, err)
	writeJSON(w, status, map[string]string{"error": msg})
}

func statusForAdapterError(op string, err error) (int, string) {
	switch {
	case errors.Is(err, msb.ErrSandboxNotFound):
		return http.StatusNotFound, "sandbox not found"
	case errors.Is(err, msb.ErrSandboxAlreadyExists):
		return http.StatusConflict, "sandbox already exists"
	case errors.Is(err, msb.ErrSandboxStillRunning):
		return http.StatusConflict, "sandbox is still running"
	default:
		return http.StatusInternalServerError, op + " failed"
	}
}
