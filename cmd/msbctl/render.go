package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Output formats for read commands.
const (
	formatTable = "table"
	formatJSON  = "json"
	formatYAML  = "yaml"
)

// renderRead renders a successful read response in the requested format. For
// json/yaml it is an opaque passthrough of the server's payload (re-encoded for
// yaml); only the table format inspects the decoded shape, and even then it only
// reads map keys for display — it owns no schema (ADR-0007).
func renderRead(w io.Writer, format string, body []byte, columns []string) error {
	switch format {
	case formatJSON:
		return writeJSONOut(w, body)
	case formatYAML:
		return writeYAMLOut(w, body)
	case formatTable, "":
		return renderTable(w, body, columns)
	default:
		return fmt.Errorf("unknown output format %q (want table, json, or yaml)", format)
	}
}

// writeJSONOut prints the server's JSON bytes verbatim, with a trailing newline.
func writeJSONOut(w io.Writer, body []byte) error {
	if _, err := w.Write(body); err != nil {
		return err
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}

// writeYAMLOut re-encodes the decoded JSON payload as YAML. The client does not
// model the payload — it decodes into a generic value purely to transcode.
func writeYAMLOut(w io.Writer, body []byte) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("decode response for yaml output: %w", err)
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode yaml output: %w", err)
	}
	_, err = w.Write(out)
	return err
}

// renderTable prints a compact human table. A JSON array becomes one row per
// element over the given columns; a JSON object becomes a key/value listing.
func renderTable(w io.Writer, body []byte, columns []string) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return renderListTable(w, trimmed, columns)
	}
	return renderObjectTable(w, trimmed)
}

func renderListTable(w io.Writer, body []byte, columns []string) error {
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return fmt.Errorf("decode list response: %w", err)
	}
	if len(rows) == 0 {
		_, err := io.WriteString(w, "(none)\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = strings.ToUpper(c)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = cellString(r[c])
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func renderObjectTable(w io.Writer, body []byte) error {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return fmt.Errorf("decode object response: %w", err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, k := range keys {
		fmt.Fprintf(tw, "%s\t%s\n", k, cellString(obj[k]))
	}
	return tw.Flush()
}

// cellString renders a decoded JSON value for a table cell. JSON numbers decode
// to float64; integral ones are printed without a decimal point or exponent so
// byte counts stay readable. Nested values fall back to compact JSON.
func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		if t == "" {
			return "-"
		}
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e18 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}
