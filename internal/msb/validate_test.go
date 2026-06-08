package msb

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Valid: leading alphanumeric, safe charset, within length.
		{"alpine", true},
		{"my-sandbox", true},
		{"my_sandbox.v2", true},
		{"A1", true},
		{"probe-snap", true},
		{"0", true},

		// Invalid: empty.
		{"", false},
		// Invalid: leading dash — the flag-injection vector. `msb rm --force`.
		{"-f", false},
		{"--force", false},
		{"--help", false},
		// Invalid: leading dot/underscore (only alnum may lead).
		{".hidden", false},
		{"_x", false},
		// Invalid: disallowed characters that could reach a shell or msb parser.
		{"a b", false},
		{"a/b", false},
		{"a:b", false},
		{"a@b", false},
		{"a=b", false},
		{"a$b", false},
		{"a\nb", false},
		{"a\x00b", false},
		// Invalid: too long (> 128 chars).
		{strings.Repeat("a", 129), false},
		// Valid: exactly 128 chars.
		{strings.Repeat("a", 128), true},
	}
	for _, tc := range cases {
		if got := ValidName(tc.in); got != tc.want {
			t.Errorf("ValidName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidImage(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Valid OCI references — looser than names, but still no leading dash.
		{"alpine", true},
		{"alpine:3.19", true},
		{"docker.io/library/alpine:3.19", true},
		{"ghcr.io/org/img@sha256:abc123", true},
		{"registry:5000/img:tag", true},

		// Invalid: empty, leading dash, whitespace, control chars.
		{"", false},
		{"-alpine", false},
		{"--rm", false},
		{"al pine", false},
		{"alpine\n", false},
		{"alpine\x00", false},
	}
	for _, tc := range cases {
		if got := ValidImage(tc.in); got != tc.want {
			t.Errorf("ValidImage(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidSize(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Valid: digits with an optional short unit suffix. msb owns the exact
		// unit grammar; we only guarantee no-leading-dash and a sane shape.
		{"1G", true},
		{"512M", true},
		{"10", true},
		{"10GiB", true},
		{"2g", true},

		// Invalid: empty, leading dash (the flag-injection vector), junk.
		{"", false},
		{"-1G", false},
		{"--size", false},
		{"1 G", false},
		{"1G;rm", false},
		{"abc", false},
	}
	for _, tc := range cases {
		if got := ValidSize(tc.in); got != tc.want {
			t.Errorf("ValidSize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
