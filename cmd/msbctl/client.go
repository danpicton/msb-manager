package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Exit codes. 0 is success; 4xx and 5xx get distinct non-zero codes so a script
// can tell a client mistake from a server fault, and anything else is generic.
const (
	exitOK          = 0
	exitGeneric     = 1
	exitClientError = 4
	exitServerError = 5
)

// requestTimeout bounds a single API call. msb-manager itself bounds the msb
// subprocess (issue #4); this is the client-side backstop against a wedged
// connection.
const requestTimeout = 60 * time.Second

// client is the thin HTTP transport to msb-manager. It is deliberately opaque:
// it sets auth and content-type, performs the call, and hands back raw status
// and bytes — it owns no response schema (ADR-0007).
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

// newClient builds a client for the resolved target. The trailing slash on the
// base URL is trimmed so path joins are predictable.
func newClient(t target) *client {
	return &client{
		baseURL: strings.TrimRight(t.URL, "/"),
		token:   t.Token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// apiResponse is the raw outcome of a call: HTTP status and body bytes. No
// decoding into typed structs — the caller renders the bytes (ADR-0007).
type apiResponse struct {
	status int
	body   []byte
}

// do issues one request. The bearer token is attached only when set, so a
// genuinely token-less call (none in practice) does not send an empty header.
// The token is never logged here or anywhere else.
func (c *client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*apiResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return &apiResponse{status: resp.StatusCode, body: data}, nil
}

// exitCodeForStatus maps an HTTP status onto a process exit code.
func exitCodeForStatus(status int) int {
	switch {
	case status >= 200 && status < 300:
		return exitOK
	case status >= 400 && status < 500:
		return exitClientError
	case status >= 500:
		return exitServerError
	default:
		return exitGeneric
	}
}

// serverErrorMessage extracts a human message from an error response body. The
// server renders errors as {"error": "..."}; fall back to the trimmed raw body
// if it is not that shape.
func serverErrorMessage(body []byte) string {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		return env.Error
	}
	return strings.TrimSpace(string(body))
}

// renderServerError writes a non-2xx response to stderr. It only ever sees the
// status and the server's body — never the token — so it cannot leak the
// credential.
func renderServerError(w io.Writer, status int, body []byte) {
	msg := serverErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	fmt.Fprintf(w, "error: %s (HTTP %d)\n", msg, status)
}

// requireToken fails with a clear message when an authenticated call has no
// token, rather than letting the request go out and surface as a bare 401. The
// message names where to put the token, not the token itself.
func requireToken(t target) error {
	if t.Token == "" {
		return errors.New("no token configured: set MSB_MANAGER_TOKEN, add one to the config-file profile, or pass --token")
	}
	return nil
}
