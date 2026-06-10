package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"msb-manager/internal/api"
	"msb-manager/internal/lock"
	"msb-manager/internal/msb"
)

func handleListVolumes(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		volumes, err := client.VolumeList(r.Context())
		if err != nil {
			writeAdapterError(w, r, "list volumes", err)
			return
		}
		writeJSON(w, http.StatusOK, api.NewVolumes(volumes))
	}
}

// volumeRequest is the create-volume body. Two fields, two units of work — no
// need for a dedicated package yet; if other endpoints grow similar shapes
// they can graduate to internal/spec.
type volumeRequest struct {
	Name string `yaml:"name" json:"name"`
	Size string `yaml:"size" json:"size"`
}

func handleCreateVolume(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSpecBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read body"})
			return
		}
		if len(body) > maxSpecBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
			return
		}

		var req volumeRequest
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.Name == "" || req.Size == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and size are required"})
			return
		}
		// Reject identifiers/sizes msb would misparse as flags (issue #3).
		if !msb.ValidName(req.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid volume name"})
			return
		}
		if !msb.ValidSize(req.Size) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid volume size"})
			return
		}

		if err := client.VolumeCreate(r.Context(), req.Name, req.Size); err != nil {
			writeAdapterError(w, r, "create volume", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "size": req.Size})
	}
}

// planVolumeBatch decides, per requested volume, what to do given the volumes
// that already exist (name -> quota in MiB, from `msb volume ls`). It performs
// NO subprocess calls — it is the pure reconcile seam (ADR-0003: msb is the
// source of truth, passed in as `existing`). The handler then executes only the
// `created` entries.
//
//   - absent                -> created
//   - present, size matches -> exists
//   - present, size differs -> error (msb can't shrink/grow; a mismatch is hard)
//
// "Same size" is a unit-normalised comparison via msb.ParseSizeMiB (1G == 1024M),
// never string equality.
func planVolumeBatch(req []volumeRequest, existing map[string]int) []api.VolumeResult {
	out := make([]api.VolumeResult, 0, len(req))
	for _, v := range req {
		res := api.VolumeResult{Name: v.Name, Size: v.Size}
		wantMiB, err := msb.ParseSizeMiB(v.Size)
		if err != nil {
			res.Status = api.VolumeStatusError
			res.Error = err.Error()
			out = append(out, res)
			continue
		}
		switch haveMiB, ok := existing[v.Name]; {
		case !ok:
			res.Status = api.VolumeStatusCreated
		case haveMiB == wantMiB:
			res.Status = api.VolumeStatusExists
		default:
			res.Status = api.VolumeStatusError
			res.Error = fmt.Sprintf("exists at %dMiB, cannot resize to %dMiB", haveMiB, wantMiB)
		}
		out = append(out, res)
	}
	return out
}

func handleDeleteVolume(client MsbClient, vlock *lock.VolumeLock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		// msb itself will remove a volume out from under a running sandbox
		// (verified msb v0.5.2). Refuse here when our lock shows it's in use.
		if holder, ok := vlock.Holder(name); ok {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("volume %q in use by running sandbox %q", name, holder),
			})
			return
		}
		if err := client.VolumeRm(r.Context(), name); err != nil {
			writeAdapterError(w, r, "remove volume", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
