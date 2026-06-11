package msb

import (
	"fmt"
	"strconv"
	"strings"
)

// unitMiB maps a size suffix (lower-cased) to its multiplier in MiB. Every
// spelling is treated as a binary multiple of a MiB because msb reports a
// volume's quota as quota_mib (binary MiB); normalising every requested size
// onto that single unit is what turns a "same size" check into a numeric
// comparison rather than a brittle string match. A bare number is already MiB.
var unitMiB = map[string]int{
	"":    1,
	"m":   1,
	"mb":  1,
	"mib": 1,
	"g":   1024,
	"gb":  1024,
	"gib": 1024,
	"t":   1024 * 1024,
	"tb":  1024 * 1024,
	"tib": 1024 * 1024,
}

// ParseSizeMiB normalises an msb volume size (e.g. "1G", "512M", "2048") into
// whole MiB — the unit msb reports as quota_mib. It is the single place that
// turns the human size grammar into a number, so callers can compare a
// requested size against an existing quota numerically (1G == 1024M) instead of
// by string. Callers should ValidSize first; ParseSizeMiB rejects anything it
// cannot normalise (no number, or a sub-MiB/unknown unit).
func ParseSizeMiB(s string) (int, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q: no leading number", s)
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	mult, ok := unitMiB[strings.ToLower(s[i:])]
	if !ok {
		return 0, fmt.Errorf("invalid size %q: unsupported unit %q", s, s[i:])
	}
	return n * mult, nil
}
