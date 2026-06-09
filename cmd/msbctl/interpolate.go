package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// placeholderRE matches a ${VAR} reference. Variable names follow the usual
// shell-ish identifier rule so a stray "${" in prose is unlikely to match.
var placeholderRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolate performs value-safe ${VAR} substitution over a YAML/JSON spec
// (ADR-0008). It parses the document to a node tree and rewrites only *scalar
// values*, never structure, so a substituted value can never introduce a new
// key, list item, or any other YAML structure — newline/metacharacter
// injection is impossible by construction (see TestInterpolate_InjectionImpossible).
//
// Substituted scalars are forced to double-quoted strings, which is both the
// safe representation and the correct one for the secret/token values this is
// built for. A reference to an undefined variable is a hard error naming the
// variable, rather than a silently-empty substitution.
//
// This is not schema parsing (ADR-0007): the client walks a generic node tree
// and knows nothing of the spec's fields.
func interpolate(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec for interpolation: %w", err)
	}

	missing := map[string]struct{}{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && placeholderRE.MatchString(n.Value) {
			n.Value = placeholderRE.ReplaceAllStringFunc(n.Value, func(m string) string {
				name := placeholderRE.FindStringSubmatch(m)[1]
				v, ok := lookup(name)
				if !ok {
					missing[name] = struct{}{}
					return m
				}
				return v
			})
			// Pin the result as a quoted string so the value can never be
			// re-interpreted as structure or a non-string scalar type.
			n.Tag = "!!str"
			n.Style = yaml.DoubleQuotedStyle
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)

	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for k := range missing {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("undefined variable(s) referenced in spec: %s "+
			"(set via the environment or --set NAME=VALUE)", strings.Join(names, ", "))
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("re-encode interpolated spec: %w", err)
	}
	return out, nil
}

// makeLookup composes the interpolation value sources: explicit --set entries
// take precedence over the process environment (flags override env, ADR-0008).
// An environment variable that is unset *or empty* is treated as undefined, so
// a blank secret fails loudly rather than silently substituting nothing.
func makeLookup(set map[string]string, getenv func(string) string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if v, ok := set[name]; ok {
			return v, true
		}
		if v := getenv(name); v != "" {
			return v, true
		}
		return "", false
	}
}
