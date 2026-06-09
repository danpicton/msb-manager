package server

import (
	"net/http"
	"strconv"

	"msb-manager/internal/api"
	"msb-manager/internal/msb"
)

// handleLogs proxies `msb logs <name> --json` and returns the raw NDJSON
// stream. Query params (tail, since, until, source, grep) map 1:1 to msb's
// flags. The body is opaque from our perspective — one JSON object per line,
// shape owned by msb.
func handleLogs(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		q := r.URL.Query()
		opts := msb.LogsOpts{
			Since:  q.Get("since"),
			Until:  q.Get("until"),
			Source: q.Get("source"),
			Grep:   q.Get("grep"),
		}
		if tail := q.Get("tail"); tail != "" {
			n, err := strconv.Atoi(tail)
			if err != nil || n < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "tail must be a non-negative integer",
				})
				return
			}
			opts.Tail = n
		}

		body, err := client.Logs(r.Context(), name, opts)
		if err != nil {
			writeAdapterError(w, r, "read logs", err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// handleMetrics returns the parsed point-in-time metrics object as JSON.
func handleMetrics(client MsbClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validPathName(w, name) {
			return
		}
		m, err := client.Metrics(r.Context(), name)
		if err != nil {
			writeAdapterError(w, r, "read metrics", err)
			return
		}
		writeJSON(w, http.StatusOK, api.NewMetrics(m))
	}
}
