package ingest

import "testing"

// TestVersionRank covers the order versions are read in, and the values that
// are not versions at all: the corpus writes dates where a version would go.
func TestVersionRank(t *testing.T) {
	cases := []struct {
		version string
		want    int
	}{
		{"V1.0.0", 1000},
		{"1.1.0", 1010},
		{"1.0.1", 1001},
		{"2.0", 2000},
		{"1.11.2", 1112},
		{"v 1.2.0", 1020},
		{"V 1.1.0", 1010},
		{"", -1},
		{"  ", -1},
		{"V", -1},
		{"初版", -1},
		{"2023.12", -1},
		{"2023.11.28", -1},
		{"24.04.29", -1},
		{"1.2.3.4", -1},
		{"1.x.0", -1},
		{"—", -1},
	}
	for _, c := range cases {
		if got := VersionRank(c.version); got != c.want {
			t.Errorf("VersionRank(%q) = %d; want %d", c.version, got, c.want)
		}
	}
}
