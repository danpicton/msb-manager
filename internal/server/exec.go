package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"msb-manager/internal/api"
)

// execRequest is the POST /sandboxes/{name}/exec body: the argv to run inside
// the sandbox. Argv elements are deliberately not identifier-validated — they
// are an arbitrary guest command, and the adapter passes them after a `--`
// terminator so msb can never parse them as its own flags (issue #3). Per-call
// env is deliberately absent: prefix the command with `env KEY=VALUE` instead
// of growing a second env surface.
type execRequest struct {
	Command []string `yaml:"command" json:"command"`
}

// parseExecRequest decodes a YAML (or JSON) exec body, rejecting unknown
// fields so a typo surfaces as an error rather than a silent drop (matching
// spec.Parse and parseVolumeManifest).
func parseExecRequest(data []byte) (execRequest, error) {
	var q execRequest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&q); err != nil {
		return execRequest{}, fmt.Errorf("parse exec request: %w", err)
	}
	return q, nil
}

func (q execRequest) validate() error {
	if len(q.Command) == 0 {
		return errors.New("command is required and must be a non-empty list")
	}
	if q.Command[0] == "" {
		return errors.New("command[0] must not be empty")
	}
	return nil
}

// handleExecSandbox runs a command inside a running sandbox and returns its
// captured stdout/stderr and exit code. Request/response only — no TTY, no
// streaming — bounded by the msb per-invocation timeout, so it is for short
// commands; interactive sessions belong to the data plane (SSH). The command
// exiting non-zero is HTTP 200 with the exit_code in the body: the control
// plane succeeded at running it.
func handleExecSandbox(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSpecBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read body"})
			return
		}
		if len(body) > maxSpecBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "exec request too large"})
			return
		}
		req, err := parseExecRequest(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := req.validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := client.Exec(r.Context(), name, req.Command)
		if err != nil {
			writeAdapterError(w, r, "exec in sandbox", err)
			return
		}
		writeJSON(w, http.StatusOK, api.NewExecResult(res))
	}
}
