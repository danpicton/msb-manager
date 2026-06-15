package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"msb-manager/internal/api"
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
		// Map adapter types onto the public DTO before the wire (ADR-0006).
		// NewSandboxSummaries returns a non-nil slice so empty serialises as [].
		writeJSON(w, http.StatusOK, api.NewSandboxSummaries(sandboxes))
	}
}

func handleInspectSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		detail, err := client.Inspect(r.Context(), name)
		if err != nil {
			writeAdapterError(w, r, "inspect sandbox", err)
			return
		}
		writeJSON(w, http.StatusOK, api.NewSandboxDetail(detail))
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
		// roll back so the volume isn't held by a sandbox that doesn't exist —
		// but only the claims this request newly made: a retried create of an
		// already-running sandbox re-claims its volume idempotently, and
		// releasing that pre-existing claim would free the volume for a
		// double-mount (issue #19).
		volumes := volumesFromSpec(s)
		newlyClaimed, err := vlock.AcquireMany(volumes, s.Name)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err := client.Create(r.Context(), s.ToCreateOpts()); err != nil {
			vlock.ReleaseVolumes(newlyClaimed, s.Name)
			writeAdapterError(w, r, "create sandbox", err)
			return
		}
		resp := map[string]string{"name": s.Name}
		if s.Image != "" {
			resp["image"] = s.Image
		} else {
			resp["snapshot"] = s.Snapshot
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

func handleStartSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}

		// Look up the sandbox's volumes so we can claim them. A Start of a
		// missing sandbox surfaces as msb.ErrSandboxNotFound -> 404.
		detail, err := client.Inspect(r.Context(), name)
		if err != nil {
			writeAdapterError(w, r, "start sandbox", err)
			return
		}
		// As with create, roll back only the claims this request newly made —
		// a failed start of an already-running sandbox must not strip the
		// running instance's claims (issue #19).
		volumes := detail.VolumeNames()
		newlyClaimed, err := vlock.AcquireMany(volumes, name)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err := client.Start(r.Context(), name); err != nil {
			vlock.ReleaseVolumes(newlyClaimed, name)
			writeAdapterError(w, r, "start sandbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleStopSandbox(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		if err := client.Stop(r.Context(), name); err != nil {
			// A "sandbox not found" from msb is authoritative proof the sandbox
			// isn't running (e.g. removed out-of-band), so releasing its claim
			// here is strictly safe and stops the volume leaking (issue #21).
			if errors.Is(err, msb.ErrSandboxNotFound) {
				vlock.Release(name)
			}
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
		if !validPathName(w, name) {
			return
		}
		if err := client.Rm(r.Context(), name); err != nil {
			// As with stop, a "sandbox not found" proves the sandbox is gone, so
			// release its claim before the 404 rather than leaking it (issue #21).
			if errors.Is(err, msb.ErrSandboxNotFound) {
				vlock.Release(name)
			}
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
	case errors.Is(err, msb.ErrVolumeAlreadyExists):
		return http.StatusConflict, "volume already exists"
	case errors.Is(err, msb.ErrVolumeNotFound):
		return http.StatusNotFound, "volume not found"
	case errors.Is(err, msb.ErrSnapshotAlreadyExists):
		return http.StatusConflict, "snapshot already exists"
	case errors.Is(err, msb.ErrSnapshotNotFound):
		return http.StatusNotFound, "snapshot not found"
	case errors.Is(err, msb.ErrTimeout):
		// A wedged msb call hit its bound (issue #4); surface it as an upstream
		// timeout, not a generic 500.
		return http.StatusGatewayTimeout, "msb command timed out"
	default:
		return http.StatusInternalServerError, op + " failed"
	}
}
