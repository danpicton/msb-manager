package msb

import "testing"

func TestParseSizeMiB(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"2048", 2048, false}, // bare number is already MiB
		{"512M", 512, false},
		{"512MB", 512, false},
		{"512MiB", 512, false},
		{"1G", 1024, false},   // binary: 1G == 1024 MiB
		{"1GB", 1024, false},  // every spelling normalises to binary MiB
		{"1GiB", 1024, false}, //
		{"10G", 10240, false},
		{"1T", 1024 * 1024, false},
		{"1g", 1024, false}, // case-insensitive suffix
		{"", 0, true},       // no number
		{"G", 0, true},      // no number
		{"1K", 0, true},     // sub-MiB unit not supported
		{"1Q", 0, true},     // unknown unit
	}
	for _, c := range cases {
		got, err := ParseSizeMiB(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseSizeMiB(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSizeMiB(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSizeMiB(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// The whole point of normalisation: equal real sizes expressed in different
// units compare equal once parsed, where a string match would say they differ.
func TestParseSizeMiB_EquivalentUnitsMatch(t *testing.T) {
	g, _ := ParseSizeMiB("1G")
	m, _ := ParseSizeMiB("1024M")
	if g != m {
		t.Errorf("1G (%d) and 1024M (%d) should normalise equal", g, m)
	}
}
