package main

import "testing"

func TestMaidenheadToNumber(t *testing.T) {
	tests := []struct {
		locator string
		want    int
	}{
		// 4 char
		{"AA00", 1},
		{"RR99", 32400},
		{"FN31", 10332},
		{"JO22", 17623},

		// 6 char
		{"AA00AA", 1},
		{"RR99XX", 18662400},
		{"FN31PR", 5951034},

		// 8 char
		{"AA00AA00", 1},
		{"RR99XX99", 1866240000},
		{"FN31PR42", 595103343},

		// 10 char
		{"AA00AA00AA", 1},
		{"RR99XX99XX", 1074954240000},
		{"FN31PR42MA", 342779525281},

		// 12 char
		{"AA00AA00AA00", 1},
		{"RR99XX99XX99", 107495424000000},
		{"FN31PR42MA99", 34277952528100},
	}

	for _, tt := range tests {
		t.Run(tt.locator, func(t *testing.T) {
			got := maidenheadToNumber(tt.locator)
			if got != tt.want {
				t.Errorf("maidenheadToNumber(%q) = %d, want %d", tt.locator, got, tt.want)
			}
		})
	}
}

func TestNumberToMaidenhead(t *testing.T) {
	tests := []struct {
		number int
		length int
		want   string
	}{
		// 4 char
		{1, 4, "AA00"},
		{32400, 4, "RR99"},
		{10332, 4, "FN31"},

		// 6 char
		{1, 6, "AA00AA"},
		{18662400, 6, "RR99XX"},
		{5951034, 6, "FN31PR"},

		// 8 char
		{1, 8, "AA00AA00"},
		{1866240000, 8, "RR99XX99"},
		{595103343, 8, "FN31PR42"},

		// 10 char
		{1, 10, "AA00AA00AA"},
		{1074954240000, 10, "RR99XX99XX"},
		{342779525281, 10, "FN31PR42MA"},

		// 12 char
		{1, 12, "AA00AA00AA00"},
		{107495424000000, 12, "RR99XX99XX99"},
		{34277952528100, 12, "FN31PR42MA99"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := numberToMaidenhead(tt.number, tt.length)
			if got != tt.want {
				t.Errorf("numberToMaidenhead(%d, %d) = %q, want %q", tt.number, tt.length, got, tt.want)
			}
		})
	}
}

func TestMaidenheadRoundTrip(t *testing.T) {
	locators := []string{
		"FN31", "JO22", "AA00", "RR99",
		"FN31PR", "JO22AB", "AA00AA", "RR99XX",
		"FN31PR42", "JO22AB99", "AA00AA00", "RR99XX99",
		"FN31PR42MA", "JO22AB99XX", "AA00AA00AA", "RR99XX99XX",
		"FN31PR42MA99", "JO22AB99XX00", "AA00AA00AA00", "RR99XX99XX99",
	}

	for _, loc := range locators {
		t.Run(loc, func(t *testing.T) {
			n := maidenheadToNumber(loc)
			got := numberToMaidenhead(n, len(loc))
			if got != loc {
				t.Errorf("round-trip failed: %q → %d → %q", loc, n, got)
			}
		})
	}
}

func TestMaidenheadCaseInsensitive(t *testing.T) {
	tests := []struct {
		a, b string
	}{
		{"FN31", "fn31"},
		{"FN31PR", "fn31pr"},
		{"FN31PR", "Fn31Pr"},
		{"FN31PR42MA", "fn31pr42ma"},
	}

	for _, tt := range tests {
		t.Run(tt.a+"="+tt.b, func(t *testing.T) {
			na := maidenheadToNumber(tt.a)
			nb := maidenheadToNumber(tt.b)
			if na != nb {
				t.Errorf("maidenheadToNumber(%q) = %d, maidenheadToNumber(%q) = %d, want equal", tt.a, na, tt.b, nb)
			}
		})
	}
}

func TestMaidenheadZeroReserved(t *testing.T) {
	// 0 means "no grid", so all valid locators should encode to > 0
	locators := []string{"AA00", "AA00AA", "AA00AA00", "AA00AA00AA", "AA00AA00AA00"}
	for _, loc := range locators {
		n := maidenheadToNumber(loc)
		if n <= 0 {
			t.Errorf("maidenheadToNumber(%q) = %d, want > 0", loc, n)
		}
	}
}
