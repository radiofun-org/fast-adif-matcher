package main

import (
	"strings"
	"testing"

	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
)

// makeTestRecord builds an ADIF record from a map of field name → value.
func makeTestRecord(fields map[string]string) *adif.Record {
	r := adif.NewRecord()
	for k, v := range fields {
		_ = r.Set(adif.Field{Name: strings.ToUpper(k), Value: v})
	}
	return r
}

// callsMap returns a minimal callsign map for tests that call recordToContact.
func callsMap() map[string]int64 {
	return map[string]int64{"W1AW": 1, "K3LR": 2}
}

func TestWithinThirtyMinutes(t *testing.T) {
	var tests = []struct {
		name   string
		a      Contact
		b      Contact
		answer bool
	}{
		{
			"5 minute difference",
			Contact{Date: 25000, Time: 1005},
			Contact{Date: 25000, Time: 1000},
			true,
		},
		{
			"2 day difference",
			Contact{Date: 25002, Time: 1005},
			Contact{Date: 25000, Time: 1000},
			false,
		},
		{
			"30 minute difference",
			Contact{Date: 25000, Time: 1005},
			Contact{Date: 25000, Time: 1035},
			true,
		},
		{
			"31 minute difference",
			Contact{Date: 25000, Time: 1005},
			Contact{Date: 25000, Time: 1036},
			false,
		},
		{
			"11 minute difference on previous day",
			Contact{Date: 25000, Time: 10},
			Contact{Date: 24999, Time: 1439},
			true,
		},
		{
			"31 minute difference on previous day",
			Contact{Date: 25000, Time: 30},
			Contact{Date: 24999, Time: 1439},
			false,
		},
		{
			"20 minute difference on next day",
			Contact{Date: 25000, Time: 1430},
			Contact{Date: 25001, Time: 10},
			true,
		},
		{
			"40 minute difference on next day",
			Contact{Date: 25000, Time: 1430},
			Contact{Date: 25001, Time: 30},
			false,
		},
		{
			"time_off overlaps other time_on",
			Contact{Date: 25000, Time: 600, TimeOff: 640},
			Contact{Date: 25000, Time: 635},
			true,
		},
		{
			"time_off brings within 30 min",
			Contact{Date: 25000, Time: 600, TimeOff: 640},
			Contact{Date: 25000, Time: 660},
			true,
		},
		{
			"time_off still too far",
			Contact{Date: 25000, Time: 600, TimeOff: 620},
			Contact{Date: 25000, Time: 660},
			false,
		},
		{
			"both have time_off, ranges overlap",
			Contact{Date: 25000, Time: 600, TimeOff: 640},
			Contact{Date: 25000, Time: 630, TimeOff: 660},
			true,
		},
		{
			"time_off crosses midnight",
			Contact{Date: 25000, Time: 1420, TimeOff: 10},
			Contact{Date: 25001, Time: 20},
			true,
		},
		{
			"time_off crosses midnight, too far",
			Contact{Date: 25000, Time: 1420, TimeOff: 10},
			Contact{Date: 25001, Time: 50},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if withinThirtyMinutes(tt.a, tt.b) != tt.answer {
				t.Errorf("got %t, want %t", !tt.answer, tt.answer)
			}
		})
	}
}

func initSubmodeToMode() {
	if len(submodeToMode) > 0 {
		return
	}
	for _, v := range spec.SubmodeEnumeration.Values {
		sub := v.(spec.SubmodeEnum)
		submodeToMode[strings.ToUpper(sub.Submode)] = strings.ToUpper(sub.Mode)
	}
}

func TestInferModeFromSubmode(t *testing.T) {
	initSubmodeToMode()

	tests := []struct {
		name     string
		fields   map[string]string
		wantMode int
	}{
		{
			"USB infers SSB",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"SUBMODE":  "USB",
			},
			Modes["SSB"],
		},
		{
			"FT4 infers MFSK",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"SUBMODE":  "FT4",
			},
			Modes["MFSK"],
		},
		{
			"explicit mode not overridden",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "CW",
				"SUBMODE":  "PCW",
			},
			Modes["CW"],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := recordToContact(1, makeTestRecord(tt.fields), callsMap())
			if err != nil {
				t.Fatalf("recordToContact: %v", err)
			}
			if c.Mode != tt.wantMode {
				t.Errorf("Mode = %d, want %d", c.Mode, tt.wantMode)
			}
		})
	}
}

func TestInferDXCCFromCountry(t *testing.T) {
	tests := []struct {
		name     string
		fields   map[string]string
		wantDXCC int
	}{
		{
			"USA from country name",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"COUNTRY":  "UNITED STATES OF AMERICA",
			},
			291,
		},
		{
			"France from country name",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"COUNTRY":  "FRANCE",
			},
			227,
		},
		{
			"unknown country returns 0",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"COUNTRY":  "NONEXISTENT LAND",
			},
			0,
		},
		{
			"explicit DXCC not overridden",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "227",
				"COUNTRY":  "UNITED STATES OF AMERICA",
			},
			227,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := recordToContact(1, makeTestRecord(tt.fields), callsMap())
			if err != nil {
				t.Fatalf("recordToContact: %v", err)
			}
			if c.DXCC != tt.wantDXCC {
				t.Errorf("DXCC = %d, want %d", c.DXCC, tt.wantDXCC)
			}
		})
	}
}

func TestInferCQZoneFromDXCC(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]string
		wantZoneCQ int
	}{
		{
			"single-zone country (France)",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "227",
			},
			14,
		},
		{
			"multi-zone with state CA",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
				"MY_STATE": "CA",
			},
			3,
		},
		{
			"multi-zone with state NY",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
				"MY_STATE": "NY",
			},
			5,
		},
		{
			"multi-zone no state returns 0",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
			},
			0,
		},
		{
			"explicit zone not overridden",
			map[string]string{
				"CALL":       "W1AW",
				"QSO_DATE":   "20240101",
				"TIME_ON":    "1200",
				"MODE":       "SSB",
				"DXCC":       "227",
				"MY_CQ_ZONE": "99",
			},
			99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := recordToContact(1, makeTestRecord(tt.fields), callsMap())
			if err != nil {
				t.Fatalf("recordToContact: %v", err)
			}
			if c.ZoneCQ != tt.wantZoneCQ {
				t.Errorf("ZoneCQ = %d, want %d", c.ZoneCQ, tt.wantZoneCQ)
			}
		})
	}
}

func TestInferITUZoneFromDXCC(t *testing.T) {
	tests := []struct {
		name        string
		fields      map[string]string
		wantZoneITU int
	}{
		{
			"single-zone country (France)",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "227",
			},
			27,
		},
		{
			"multi-zone with state CA",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
				"MY_STATE": "CA",
			},
			6,
		},
		{
			"multi-zone with state NY",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
				"MY_STATE": "NY",
			},
			8,
		},
		{
			"multi-zone no state returns 0",
			map[string]string{
				"CALL":     "W1AW",
				"QSO_DATE": "20240101",
				"TIME_ON":  "1200",
				"MODE":     "SSB",
				"DXCC":     "291",
			},
			0,
		},
		{
			"explicit zone not overridden",
			map[string]string{
				"CALL":        "W1AW",
				"QSO_DATE":    "20240101",
				"TIME_ON":     "1200",
				"MODE":        "SSB",
				"DXCC":        "227",
				"MY_ITU_ZONE": "99",
			},
			99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := recordToContact(1, makeTestRecord(tt.fields), callsMap())
			if err != nil {
				t.Fatalf("recordToContact: %v", err)
			}
			if c.ZoneITU != tt.wantZoneITU {
				t.Errorf("ZoneITU = %d, want %d", c.ZoneITU, tt.wantZoneITU)
			}
		})
	}
}

func TestParseADIFLocation(t *testing.T) {
	tests := []struct {
		input   string
		want    float64
		wantErr bool
	}{
		{"N045 20.000", 45.333333, false},
		{"S045 20.000", -45.333333, false},
		{"E075 30.000", 75.5, false},
		{"W075 30.000", -75.5, false},
		{"N000 00.000", 0, false},
		{"X045 20.000", 0, true},
		{"", 0, true},
		{"N", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseADIFLocation(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %t", err, tt.wantErr)
			}
			if !tt.wantErr {
				diff := got - tt.want
				if diff < -0.001 || diff > 0.001 {
					t.Errorf("got %f, want %f", got, tt.want)
				}
			}
		})
	}
}

func TestLatLonToMaidenhead(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
		want     string // 4-char prefix to check
	}{
		{"FN31 (Connecticut area)", 41.5, -72.5, "FN31"},
		{"JN18 (Paris area)", 48.85, 2.35, "JN18"},
		{"QF56 (Sydney area)", -33.87, 151.21, "QF56"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latLonToMaidenhead(tt.lat, tt.lon)
			if got[:4] != tt.want {
				t.Errorf(
					"latLonToMaidenhead(%f, %f) = %q, want prefix %q",
					tt.lat,
					tt.lon,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestInferGridFromLatLon(t *testing.T) {
	c, err := recordToContact(1, makeTestRecord(map[string]string{
		"CALL": "W1AW", "QSO_DATE": "20240101", "TIME_ON": "1200", "MODE": "SSB",
		"MY_LAT": "N041 42.000", "MY_LON": "W072 44.000",
	}), callsMap())
	if err != nil {
		t.Fatalf("recordToContact: %v", err)
	}
	if c.Grid == 0 {
		t.Fatal("expected Grid to be inferred from lat/lon")
	}
	if c.GridSize != 6 {
		t.Errorf("GridSize = %d, want 6", c.GridSize)
	}
	// Should be in the FN31 area
	grid := numberToMaidenhead(c.Grid, c.GridSize)
	if grid[:4] != "FN31" {
		t.Errorf("inferred grid %q, expected FN31xx", grid)
	}
}

func TestBandsMapMatchesSpec(t *testing.T) {
	specBands := map[string]bool{}
	for _, v := range spec.BandEnumeration.Values {
		b := v.(spec.BandEnum)
		specBands[strings.ToLower(b.Band)] = true
	}

	for name := range Bands {
		if !specBands[name] {
			t.Errorf("Bands map has %q but spec does not", name)
		}
	}
	for name := range specBands {
		if _, ok := Bands[name]; !ok {
			t.Errorf("spec has band %q but Bands map does not", name)
		}
	}
}

func TestSplitPotaRefs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"K-0059", []string{"K-0059"}},
		{"K-0059,K-1234", []string{"K-0059", "K-1234"}},
		{"K-0059, K-1234", []string{"K-0059", "K-1234"}},
		{"K-0059,,K-1234", []string{"K-0059", "K-1234"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitPotaRefs(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
