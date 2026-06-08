package server

import (
	"bytes"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"msb-manager/internal/msb"
)

// snapshotRequest is the create-snapshot body. `from` is the source sandbox
// (must be stopped); `name` is the snapshot destination. Labels are arbitrary
// key/value strings; the client owns whatever convention sits there (incl.
// msb.parent for lineage). `force` overwrites an existing artifact of the
// same name.
type snapshotRequest struct {
	From   string            `yaml:"from" json:"from"`
	Name   string            `yaml:"name" json:"name"`
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Force  bool              `yaml:"force,omitempty" json:"force,omitempty"`
}

func handleListSnapshots(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshots, err := client.SnapshotList(r.Context())
		if err != nil {
			writeAdapterError(w, r, "list snapshots", err)
			return
		}
		if snapshots == nil {
			snapshots = []msb.Snapshot{}
		}
		writeJSON(w, http.StatusOK, snapshots)
	}
}

func handleCreateSnapshot(client MsbClient) http.HandlerFunc {
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

		var req snapshotRequest
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.From == "" || req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and name are required"})
			return
		}
		// Reject identifiers msb would misparse as flags (issue #3). Both `from`
		// (source sandbox) and `name` (snapshot dest) are plain identifiers.
		if !msb.ValidName(req.From) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid from"})
			return
		}
		if !msb.ValidName(req.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		// Label keys are emitted as `--label <key>=<val>`; a key like `--force`
		// would reach msb as a flag-shaped arg (review #4). Values are glued
		// after `<key>=` into a single arg so can't become a flag — only keys
		// need the identifier check.
		for k := range req.Labels {
			if !msb.ValidName(k) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid label key"})
				return
			}
		}

		if err := client.SnapshotCreate(r.Context(), req.From, req.Name, req.Labels, req.Force); err != nil {
			writeAdapterError(w, r, "create snapshot", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "from": req.From})
	}
}

func handleDeleteSnapshot(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		if err := client.SnapshotRm(r.Context(), name); err != nil {
			writeAdapterError(w, r, "remove snapshot", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
