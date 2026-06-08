package msb

import "regexp"

// Identifier validation guards the subprocess boundary against argument/flag
// injection (issue #3). Names arrive from clients (spec body and URL path
// segments) and are passed to the `msb` CLI as positional/flag arguments. We
// use exec.CommandContext (no shell), so this is not classic shell injection —
// but an identifier beginning with '-' is parsed by msb as a *flag*, not a
// value (e.g. a sandbox named "--force" reaching `msb rm --force`). Rejecting
// malformed identifiers before they reach the adapter closes that vector and
// keeps the control plane a constrained surface.
//
// These are the single source of truth for "what shape of identifier the
// adapter accepts"; spec.Validate() and the path-name handlers both call them.
var (
	// nameRe is the safe identifier shape: a leading alphanumeric (never '-',
	// '.', or '_'), then up to 127 more chars from a small safe set. Covers
	// sandbox/volume/snapshot names.
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

	// imageRe is deliberately looser than nameRe — OCI references carry '/',
	// ':', '@', and '.' (registry/repo:tag@digest) — but still forbids a
	// leading '-' and any whitespace/control character. First char is a safe
	// alphanumeric or '.' / '_' (some local refs), never '-'.
	imageRe = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/:@-]*$`)

	// sizeRe matches msb volume sizes: digits then an optional short unit
	// suffix (G, GB, GiB, M, MiB, …). Starting with a digit inherently forbids
	// a leading '-'; msb owns the exact unit grammar and rejects bad units.
	sizeRe = regexp.MustCompile(`^[0-9]+[A-Za-z]{0,3}$`)
)

// ValidName reports whether s is a well-formed sandbox/volume/snapshot
// identifier: non-empty, no leading '-', within the documented safe charset,
// at most 128 characters.
func ValidName(s string) bool {
	return nameRe.MatchString(s)
}

// ValidImage reports whether s is an acceptable image reference: non-empty, no
// leading '-', no whitespace or control characters. Looser than ValidName to
// allow registry/repo:tag@digest forms.
func ValidImage(s string) bool {
	return imageRe.MatchString(s)
}

// ValidSize reports whether s is an acceptable volume size: digits with an
// optional short unit suffix and, crucially, no leading '-'.
func ValidSize(s string) bool {
	return sizeRe.MatchString(s)
}
