package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/skrushinsky/scaliger/julian"
)

// Contact is the compact in-memory representation of an ADIF QSO record.
// Categorical fields (Band, Mode, etc.) are stored as integer IDs that map
// to the lookup tables below. Date is days since 1950-01-01, Time is minutes
// from midnight. Grid is a Maidenhead locator encoded as a single integer
// (see maidenhead.go).
type Contact struct {
	ID                      int64
	Date                    int // days since 1950-01-01 (Julian difference)
	Time                    int // minutes from midnight (0–1439)
	TimeOff                 int
	FromID                  int64 // callsign ID of the log owner
	ToID                    int64 // callsign ID of the other station
	Band                    int
	Mode                    int
	SubMode                 int
	Grid                    int
	GridSize                int
	ZoneCQ                  int
	ZoneITU                 int
	DXCC                    int
	AdministrativePrimary   string // state/province
	AdministrativeSecondary string // county
	TransmitPower           int
	MyPOTA                  []string
	MySOTA                  string
	MyWWFF                  string
	MyIOTA                  string
	MySig                   string
	MySigInfo               string
}

// Modes, SubModes, ModeGroups, and Bands map ADIF enumeration strings to
// compact integer IDs for database storage. Entries marked "not an actual
// ADIF mode" are non-standard values commonly found in real-world log files.

var Modes = map[string]int{
	"AM":           1,
	"AMTOR":        2,
	"AMTORFEC":     3,
	"ARDOP":        4,
	"ASCI":         5,
	"ATV":          6,
	"C4FM":         7,
	"CHIP":         8,
	"CHIP128":      9,
	"CHIP64":       10,
	"CLO":          11,
	"CLOVER":       12, // not an actual ADIF mode
	"CONTESTI":     13,
	"CW":           14,
	"DATA":         15, // not an actual ADIF mode
	"DIGITALVOICE": 16,
	"DOMINO":       17,
	"DOMINOF":      18,
	"DMR":          19,
	"DSTAR":        20,
	"DYNAMIC":      21,
	"FAX":          22,
	"FM":           23,
	"FMHELL":       24,
	"FSK31":        25,
	"FSK441":       26,
	"FST4":         27, // not an actual ADIF mode
	"FT4":          28, // not an actual ADIF mode
	"FT8":          29,
	"GTOR":         30,
	"HELL":         31,
	"HELL80":       32,
	"HFSK":         33,
	"IMAGE":        34, // not an actual ADIF mode
	"ISCAT":        35,
	"JT4":          36,
	"JT44":         37,
	"JT4A":         38,
	"JT4B":         39,
	"JT4C":         40,
	"JT4D":         41,
	"JT4E":         42,
	"JT4F":         43,
	"JT4G":         44,
	"JT65":         45,
	"JT65A":        46,
	"JT65B":        47,
	"JT65C":        48,
	"JT6M":         49,
	"JT9":          50,
	"MFSK":         51,
	"MFSK16":       52,
	"MFSK8":        53,
	"MSK144":       54,
	"MT63":         55,
	"OLIVIA":       56,
	"OPERA":        57,
	"PAC":          58,
	"PAC2":         59,
	"PAC3":         60,
	"PACKET":       61, // not an actual ADIF mode
	"PACTOR":       62, // not an actual ADIF mode
	"PAX":          63,
	"PAX2":         64,
	"PCW":          65,
	"PHONE":        66, // not an actual ADIF mode
	"PKT":          67,
	"PSK":          68,
	"PSK10":        69,
	"PSK125":       70,
	"PSK2K":        71,
	"PSK31":        72,
	"PSK63":        73,
	"PSK63F":       74,
	"PSKAM":        75, // not an actual ADIF mode
	"PSKAM10":      76,
	"PSKAM31":      77,
	"PSKAM50":      78,
	"PSKFEC31":     79,
	"PSKHELL":      80,
	"Q15":          81,
	"Q65":          82, // not an actual ADIF mode
	"QPSK125":      83,
	"QPSK31":       84,
	"QPSK63":       85,
	"QRA64":        86,
	"ROS":          87,
	"RTTY":         88,
	"RTTYM":        89,
	"SSB":          90,
	"SSTV":         91,
	"T10":          92,
	"THOR":         93,
	"THRB":         94,
	"THRBX":        95,
	"THROB":        96, // not an actual ADIF mode
	"TOR":          97,
	"V4":           98,
	"VOI":          99,
	"WINMOR":       100,
	"WSPR":         101,
	"FSK":          102,
	"MTONE":        103,
}

var SubModes = map[string]int{
	"8PSK1000":       1,
	"8PSK1000F":      2,
	"8PSK1200F":      3,
	"8PSK125":        4,
	"8PSK125F":       5,
	"8PSK125FL":      6,
	"8PSK250":        7,
	"8PSK250F":       8,
	"8PSK250FL":      9,
	"8PSK500":        10,
	"8PSK500F":       11,
	"AMTORFEC":       12,
	"ASCI":           13,
	"C4FM":           14,
	"CHIP128":        15,
	"CHIP64":         16,
	"DMR":            17,
	"DOM-M":          18,
	"DOM11":          19,
	"DOM16":          20,
	"DOM22":          21,
	"DOM4":           22,
	"DOM44":          23,
	"DOM5":           24,
	"DOM8":           25,
	"DOM88":          26,
	"DOMINOEX":       27,
	"DOMINOF":        28,
	"DSTAR":          29,
	"FMHELL":         30,
	"FREEDV":         31,
	"FSK31":          32,
	"FSKHELL":        33,
	"FSQCALL":        34,
	"FST4":           35,
	"FST4W":          36,
	"FT4":            37,
	"GTOR":           38,
	"HELL80":         39,
	"HELLX5":         40,
	"HELLX9":         41,
	"HFSK":           42,
	"ISCAT-A":        43,
	"ISCAT-B":        44,
	"JS8":            45,
	"JT4A":           46,
	"JT4B":           47,
	"JT4C":           48,
	"JT4D":           49,
	"JT4E":           50,
	"JT4F":           51,
	"JT4G":           52,
	"JT65A":          53,
	"JT65B":          54,
	"JT65B2":         55,
	"JT65C":          56,
	"JT65C2":         57,
	"JT9-1":          58,
	"JT9-10":         59,
	"JT9-2":          60,
	"JT9-30":         61,
	"JT9-5":          62,
	"JT9A":           63,
	"JT9B":           64,
	"JT9C":           65,
	"JT9D":           66,
	"JT9E":           67,
	"JT9E FAST":      68,
	"JT9F":           69,
	"JT9F FAST":      70,
	"JT9G":           71,
	"JT9G FAST":      72,
	"JT9H":           73,
	"JT9H FAST":      74,
	"JTMS":           75,
	"LSB":            76,
	"M17":            77,
	"MFSK11":         78,
	"MFSK128":        79,
	"MFSK128L":       80,
	"MFSK16":         81,
	"MFSK22":         82,
	"MFSK31":         83,
	"MFSK32":         84,
	"MFSK4":          85,
	"MFSK64":         86,
	"MFSK64L":        87,
	"MFSK8":          88,
	"NAVTEX":         89,
	"OLIVIA 16/1000": 90,
	"OLIVIA 16/500":  91,
	"OLIVIA 32/1000": 92,
	"OLIVIA 4/125":   93,
	"OLIVIA 4/250":   94,
	"OLIVIA 8/250":   95,
	"OLIVIA 8/500":   96,
	"OPERA-BEACON":   97,
	"OPERA-QSO":      98,
	"PAC2":           99,
	"PAC3":           100,
	"PAC4":           101,
	"PAX2":           102,
	"PCW":            103,
	"PSK10":          104,
	"PSK1000":        105,
	"PSK1000RC2":     106,
	"PSK125":         107,
	"PSK125RC10":     108,
	"PSK125RC12":     109,
	"PSK125RC16":     110,
	"PSK125RC4":      111,
	"PSK125RC5":      112,
	"PSK250":         113,
	"PSK250RC2":      114,
	"PSK250RC3":      115,
	"PSK250RC5":      116,
	"PSK250RC6":      117,
	"PSK250RC7":      118,
	"PSK31":          119,
	"PSK500":         120,
	"PSK500RC2":      121,
	"PSK500RC3":      122,
	"PSK500RC4":      123,
	"PSK63":          124,
	"PSK63F":         125,
	"PSK63RC10":      126,
	"PSK63RC20":      127,
	"PSK63RC32":      128,
	"PSK63RC4":       129,
	"PSK63RC5":       130,
	"PSK800RC2":      131,
	"PSKAM10":        132,
	"PSKAM31":        133,
	"PSKAM50":        134,
	"PSKFEC31":       135,
	"PSKHELL":        136,
	"Q65":            137,
	"QPSK125":        138,
	"QPSK250":        139,
	"QPSK31":         140,
	"QPSK500":        141,
	"QPSK63":         142,
	"QRA64A":         143,
	"QRA64B":         144,
	"QRA64C":         145,
	"QRA64D":         146,
	"QRA64E":         147,
	"ROS-EME":        148,
	"ROS-HF":         149,
	"ROS-MF":         150,
	"SIM31":          151,
	"SITORB":         152,
	"SLOWHELL":       153,
	"THOR-M":         154,
	"THOR100":        155,
	"THOR11":         156,
	"THOR16":         157,
	"THOR22":         158,
	"THOR25X4":       159,
	"THOR4":          160,
	"THOR5":          161,
	"THOR50X1":       162,
	"THOR50X2":       163,
	"THOR8":          164,
	"THRBX":          165,
	"THRBX1":         166,
	"THRBX2":         167,
	"THRBX4":         168,
	"THROB1":         169,
	"THROB2":         170,
	"THROB4":         171,
	"USB":            172,
	"VARA FM 1200":   173,
	"VARA FM 9600":   174,
	"VARA HF":        175,
	"VARA SATELLITE": 176,
	"FSKH105":        177,
	"FSKH245":        178,
	"SCAMP_FAST":     179,
	"SCAMP_SLOW":     180,
	"SCAMP_VSLOW":    181,
	"SCAMP_OO":       182,
	"SCAMP_OO_SLW":   183,
}

var ModeGroups = map[string]string{
	"AM":           "PHONE",
	"AMTOR":        "DATA", // not an actual ADIF mode
	"AMTORFEC":     "DATA",
	"ARDOP":        "DATA",
	"ATV":          "IMAGE",
	"C4FM":         "PHONE",
	"CHIP":         "DATA",
	"CLO":          "DATA", // not an actual ADIF mode
	"CLOVER":       "DATA", // not an actual ADIF mode
	"CONTESTI":     "DATA",
	"CW":           "CW",
	"DATA":         "DATA", // not an actual ADIF mode
	"DIGITALVOICE": "PHONE",
	"DOMINO":       "DATA",
	"DSTAR":        "PHONE",
	"DYNAMIC":      "DATA",
	"FAX":          "IMAGE",
	"FM":           "PHONE",
	"FSK31":        "DATA",
	"FSK441":       "DATA",
	"FST4":         "DATA", // not an actual ADIF mode
	"FT4":          "DATA", // not an actual ADIF mode
	"FT8":          "DATA",
	"GTOR":         "DATA",
	"HELL":         "DATA",
	"HFSK":         "DATA",
	"IMAGE":        "IMAGE", // not an actual ADIF mode
	"ISCAT":        "DATA",
	"JT4":          "DATA",
	"JT44":         "DATA",
	"JT65":         "DATA",
	"JT6M":         "DATA",
	"JT9":          "DATA",
	"MFSK":         "DATA",
	"MFSK16":       "DATA",
	"MFSK8":        "DATA",
	"MSK144":       "DATA",
	"MT63":         "DATA",
	"OLIVIA":       "DATA",
	"OPERA":        "DATA",
	"PAC":          "DATA",
	"PACKET":       "DATA", // not an actual ADIF mode
	"PACTOR":       "DATA", // not an actual ADIF mode
	"PAX":          "DATA",
	"PHONE":        "PHONE", // not an actual ADIF mode
	"PKT":          "DATA",  // not an actual ADIF mode
	"PSK":          "DATA",
	"PSK10":        "DATA",
	"PSK125":       "DATA",
	"PSK2K":        "DATA",
	"PSK31":        "DATA",
	"PSK63":        "DATA",
	"PSK63F":       "DATA",
	"PSKAM":        "DATA", // not an actual ADIF mode
	"PSKFEC31":     "DATA",
	"Q15":          "DATA",
	"Q65":          "DATA", // not an actual ADIF mode
	"QRA64":        "DATA",
	"ROS":          "DATA",
	"RTTY":         "DATA",
	"RTTYM":        "DATA",
	"SSB":          "PHONE",
	"SSTV":         "IMAGE",
	"T10":          "DATA",
	"THOR":         "DATA",
	"THRB":         "DATA", // not an actual ADIF mode
	"THROB":        "DATA", // not an actual ADIF mode
	"TOR":          "DATA",
	"V4":           "DATA",
	"VOI":          "DATA",
	"WINMOR":       "DATA",
	"WSPR":         "DATA",
	"FSK":          "DATA",
	"MTONE":        "DATA",
}

// ModeGroupsById is populated at startup from ModeGroups + Modes.
// Used during matching: two contacts with different modes still match
// if they share a mode group (e.g. FT8 and FT4 are both "DATA").
var ModeGroupsById = map[int]string{}

// submodeToMode maps submode names to their parent mode (e.g. "USB" → "SSB").
// Populated at startup from the spec's submode enumeration.
var submodeToMode = map[string]string{}

var Bands = map[string]int{
	"2190m":  0,
	"630m":   1,
	"560m":   2,
	"160m":   3,
	"80m":    4,
	"60m":    5,
	"40m":    6,
	"30m":    7,
	"20m":    8,
	"17m":    9,
	"15m":    10,
	"12m":    11,
	"10m":    12,
	"8m":     13,
	"6m":     14,
	"5m":     15,
	"4m":     16,
	"2m":     17,
	"1.25m":  18,
	"70cm":   19,
	"33cm":   20,
	"23cm":   21,
	"13cm":   22,
	"9cm":    23,
	"6cm":    24,
	"3cm":    25,
	"1.25cm": 26,
	"6mm":    27,
	"4mm":    28,
	"2.5mm":  29,
	"2mm":    30,
	"1mm":    31,
	"submm":  32,
}

// StartJulian is the epoch for the Date field: 1950-01-01.
// Dates are stored as the number of days since this epoch, which fits
// in a Postgres SMALLINT for dates through ~2039.
var StartJulian = julian.CivilToJulian(julian.CivilDate{Year: 1950, Month: 1, Day: 1})

var numericPrefix = regexp.MustCompile(`^\d+`)

func dateStringToJulianDifference(date string) (int, error) {
	if len(date) != 8 {
		return 0, fmt.Errorf("wrong date format")
	}

	dateYear, err := strconv.Atoi(date[0:4])
	if err != nil {
		return 0, err
	}

	dateMonth, err := strconv.Atoi(date[4:6])
	if err != nil {
		return 0, err
	}

	dateDay, err := strconv.ParseFloat(date[6:8], 64)
	if err != nil {
		return 0, err
	}

	return int(
		julian.CivilToJulian(
			julian.CivilDate{Year: dateYear, Month: dateMonth, Day: dateDay},
		) - StartJulian,
	), nil
}

func timeStringToMinutesFromMidnight(time string) (int, error) {
	timeLength := len(time)

	if timeLength != 4 && timeLength != 6 {
		return 0, fmt.Errorf("wrong time format")
	}

	hours, err := strconv.Atoi(time[0:2])
	if err != nil {
		return 0, err
	}

	minutes, err := strconv.Atoi(time[2:4])
	if err != nil {
		return 0, err
	}

	return hours*60 + minutes, nil
}

// inferBand sets the band field on a record by looking up its frequency
// against the ADIF band plan. Some loggers omit BAND but include FREQ.
func inferBand(r *adif.Record, bandField string) bool {
	freqField := spec.FreqField.Name
	if bandField == spec.BandRxField.Name {
		freqField = spec.FreqRxField.Name
	}

	f, ok := r.Get(freqField)
	if !ok || f.Value == "" {
		return false
	}

	freq, err := strconv.ParseFloat(f.Value, 64)
	if err != nil {
		return false
	}

	for _, b := range spec.BandEnumeration.Values {
		band := b.(spec.BandEnum)

		lower, err := strconv.ParseFloat(band.LowerFreqMhz, 64)
		if err != nil {
			return false
		}
		upper, err := strconv.ParseFloat(band.UpperFreqMhz, 64)
		if err != nil {
			return false
		}

		if lower <= freq && freq <= upper {
			_ = r.Set(adif.Field{Name: bandField, Value: band.Band})
			return true
		}
	}

	return false
}

// splitPotaRefs splits a comma-separated POTA reference string (e.g.
// "K-0059,K-1234") into individual park references.
func splitPotaRefs(s string) []string {
	var refs []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			refs = append(refs, part)
		}
	}
	return refs
}

// resolveZoneByState looks up the CQ or ITU zone for a specific state within
// a DXCC entity. Returns 0 if the state isn't found or if the zone is ambiguous
// (e.g. "07/08" for states spanning multiple zones).
func resolveZoneByState(dxccCode, stateCode string, cq bool) int {
	for _, v := range spec.PrimaryAdministrativeSubdivisionEnumeration.Values {
		sub := v.(spec.PrimaryAdministrativeSubdivisionEnum)
		if sub.DxccEntityCode != dxccCode {
			continue
		}
		if !strings.EqualFold(sub.Code, stateCode) {
			continue
		}
		zoneStr := sub.ItuZone
		if cq {
			zoneStr = sub.CqZone
		}
		if strings.Contains(zoneStr, "/") {
			return 0
		}
		if zone, err := strconv.Atoi(zoneStr); err == nil {
			return zone
		}
		return 0
	}
	return 0
}

// parseADIFLocation parses an ADIF location string like "N045 20.000" or
// "W075 30.000" into decimal degrees. The format is [NSEW]DDD MM.MMM.
func parseADIFLocation(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, fmt.Errorf("location too short: %q", s)
	}

	dir := strings.ToUpper(s[:1])
	rest := strings.TrimSpace(s[1:])

	parts := strings.Fields(rest)
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected 'DDD MM.MMM', got %q", rest)
	}

	degrees, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("bad degrees %q: %v", parts[0], err)
	}

	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("bad minutes %q: %v", parts[1], err)
	}

	result := degrees + minutes/60.0

	switch dir {
	case "S", "W":
		result = -result
	case "N", "E":
		// positive
	default:
		return 0, fmt.Errorf("unknown direction %q", dir)
	}

	return result, nil
}

// recordToContact converts a parsed ADIF record into a Contact, resolving
// callsigns to database IDs and mapping categorical fields to integer codes.
func recordToContact(
	fromId int64,
	record *adif.Record,
	callsInDB map[string]int64,
) (Contact, error) {
	contact := Contact{FromID: fromId}
	var err error

	// -- Required: contacted station callsign --
	call, ok := record.Get(spec.CallField.Name)
	if !ok {
		return contact, fmt.Errorf("missing CALL in record %s", record)
	}
	contact.ToID = callsInDB[call.Value]
	if contact.ToID == 0 {
		return contact, fmt.Errorf("unknown callsign %q", call.Value)
	}

	// -- Band: try the field directly, fall back to inferring from frequency --
	if band, ok := record.Get(spec.BandField.Name); ok {
		contact.Band = Bands[strings.ToLower(band.Value)]
	} else if inferBand(record, spec.BandField.Name) {
		if band, ok := record.Get(spec.BandField.Name); ok {
			contact.Band = Bands[band.Value]
		}
	}

	// -- Date and time --
	if date, ok := record.Get(spec.QsoDateField.Name); ok {
		contact.Date, err = dateStringToJulianDifference(date.Value)
		if err != nil {
			return contact, fmt.Errorf("bad date %q: %v", date.Value, err)
		}
	}
	if timeOn, ok := record.Get(spec.TimeOnField.Name); ok {
		contact.Time, err = timeStringToMinutesFromMidnight(timeOn.Value)
		if err != nil {
			return contact, fmt.Errorf("bad time_on %q: %v", timeOn.Value, err)
		}
	}

	if timeOff, ok := record.Get(spec.TimeOffField.Name); ok {
		contact.TimeOff, err = timeStringToMinutesFromMidnight(timeOff.Value)
		if err != nil {
			return contact, fmt.Errorf("bad time_off %q: %v", timeOff.Value, err)
		}
	}

	// -- Grid: concatenate base + extension, encode as integer --
	if grid, ok := record.Get(spec.MyGridsquareField.Name); ok {
		gridValue := grid.Value
		if ext, ok := record.Get(spec.MyGridsquareExtField.Name); ok && ext.Value != "" {
			gridValue += ext.Value
		}
		if n := len(gridValue); n >= 4 && n <= 12 && n%2 == 0 {
			contact.Grid = maidenheadToNumber(gridValue)
			contact.GridSize = n
		}
	}

	// -- Mode / submode --
	if mode, ok := record.Get(spec.ModeField.Name); ok {
		contact.Mode, ok = Modes[strings.ToUpper(mode.Value)]
		if !ok {
			return contact, fmt.Errorf("unknown mode %q", mode.Value)
		}
	}
	if subMode, ok := record.Get(spec.SubmodeField.Name); ok {
		upperSubmode := strings.ToUpper(subMode.Value)
		contact.SubMode, ok = SubModes[upperSubmode]
		if !ok {
			return contact, fmt.Errorf("unknown submode %q", subMode.Value)
		}
		// Infer mode from submode if no explicit mode was set
		if contact.Mode == 0 {
			if parentMode, found := submodeToMode[upperSubmode]; found {
				contact.Mode = Modes[parentMode]
			}
		}
	}

	// -- Zones --
	if v, ok := record.Get(spec.MyCqZoneField.Name); ok && v.Value != "" {
		contact.ZoneCQ, err = strconv.Atoi(v.Value)
		if err != nil {
			return contact, fmt.Errorf("bad CQ zone %q: %v", v.Value, err)
		}
	}
	if v, ok := record.Get(spec.MyItuZoneField.Name); ok && v.Value != "" {
		contact.ZoneITU, err = strconv.Atoi(v.Value)
		if err != nil {
			return contact, fmt.Errorf("bad ITU zone %q: %v", v.Value, err)
		}
	}

	// -- State and county --
	// ADIF MY_CNTY is formatted as "ST,County Name" (e.g. "CO,Denver").
	// We strip the state prefix when MY_STATE is already set, and discard
	// the pair entirely if the county's embedded state code doesn't match
	// MY_STATE (indicates corrupt data).
	if v, ok := record.Get(spec.MyStateField.Name); ok && v.Value != "" {
		contact.AdministrativePrimary = v.Value
	}
	if v, ok := record.Get(spec.MyCntyField.Name); ok && v.Value != "" && v.Value != "USA" {
		county := v.Value
		if contact.AdministrativePrimary != "" {
			prefix := contact.AdministrativePrimary + ","
			if strings.HasPrefix(county, prefix) {
				county = strings.TrimLeft(county[len(prefix):], " ")
			}
		}
		contact.AdministrativeSecondary = county

		// Sanity check: if the county contains "XX,..." and XX != state, discard both.
		if len(contact.AdministrativePrimary) == 2 &&
			len(contact.AdministrativeSecondary) >= 3 &&
			contact.AdministrativeSecondary[2] == ',' &&
			contact.AdministrativeSecondary[0:2] != contact.AdministrativePrimary {
			contact.AdministrativePrimary = ""
			contact.AdministrativeSecondary = ""
		}
	}

	// -- TX power: extract leading digits (handles "100W", "5", etc.) --
	if v, ok := record.Get(spec.TxPwrField.Name); ok && v.Value != "" {
		if digits := numericPrefix.FindString(v.Value); digits != "" {
			contact.TransmitPower, err = strconv.Atoi(digits)
			if err != nil {
				return contact, fmt.Errorf("bad tx_pwr %q: %v", v.Value, err)
			}
		}
	}

	// -- DXCC entity --
	if v, ok := record.Get(spec.DxccField.Name); ok && v.Value != "" {
		contact.DXCC, err = strconv.Atoi(v.Value)
		if err != nil {
			return contact, fmt.Errorf("bad DXCC %q: %v", v.Value, err)
		}
	}

	// -- Special activity references (POTA, SOTA, WWFF, IOTA) --
	if v, ok := record.Get(spec.MyPotaRefField.Name); ok && v.Value != "" {
		contact.MyPOTA = splitPotaRefs(v.Value)
	}
	if v, ok := record.Get(spec.MySotaRefField.Name); ok && v.Value != "" {
		contact.MySOTA = v.Value
	}
	if v, ok := record.Get(spec.MyWwffRefField.Name); ok && v.Value != "" {
		contact.MyWWFF = v.Value
	}
	if v, ok := record.Get(spec.MyIotaField.Name); ok && v.Value != "" {
		contact.MyIOTA = v.Value
	}

	// Some loggers use the generic MY_SIG/MY_SIG_INFO fields instead of the
	// dedicated MY_POTA_REF etc. If the dedicated field is empty, populate it
	// from MY_SIG_INFO.
	if v, ok := record.Get(spec.MySigField.Name); ok && v.Value != "" {
		contact.MySig = v.Value
		if info, ok := record.Get(spec.MySigInfoField.Name); ok && info.Value != "" {
			contact.MySigInfo = info.Value
		}

		switch strings.ToUpper(contact.MySig) {
		case "POTA":
			if len(contact.MyPOTA) == 0 {
				contact.MyPOTA = splitPotaRefs(contact.MySigInfo)
			}
		case "SOTA":
			if contact.MySOTA == "" {
				contact.MySOTA = contact.MySigInfo
			}
		case "WWFF":
			if contact.MyWWFF == "" {
				contact.MyWWFF = contact.MySigInfo
			}
		case "IOTA":
			if contact.MyIOTA == "" {
				contact.MyIOTA = contact.MySigInfo
			}
		}
	}

	// -- Field inference --
	// Derive missing fields from other data present in the record.

	// Infer DXCC from country name
	if contact.DXCC == 0 {
		if v, ok := record.Get(spec.CountryField.Name); ok && v.Value != "" {
			for _, ev := range spec.CountryEnumeration.Value(v.Value) {
				country := ev.(spec.CountryEnum)
				if country.Deleted == "true" {
					continue
				}
				if code, err := strconv.Atoi(country.EntityCode); err == nil {
					contact.DXCC = code
					break
				}
			}
		}
	}

	// Infer CQ zone from DXCC (+ state for multi-zone countries)
	if contact.ZoneCQ == 0 && contact.DXCC != 0 {
		dxccStr := strconv.Itoa(contact.DXCC)
		zones := spec.CQZoneFor(dxccStr)
		if len(zones) == 1 {
			contact.ZoneCQ = zones[0]
		} else if len(zones) > 1 && contact.AdministrativePrimary != "" {
			contact.ZoneCQ = resolveZoneByState(dxccStr, contact.AdministrativePrimary, true)
		}
	}

	// Infer ITU zone from DXCC (+ state for multi-zone countries)
	if contact.ZoneITU == 0 && contact.DXCC != 0 {
		dxccStr := strconv.Itoa(contact.DXCC)
		zones := spec.ITUZoneFor(dxccStr)
		if len(zones) == 1 {
			contact.ZoneITU = zones[0]
		} else if len(zones) > 1 && contact.AdministrativePrimary != "" {
			contact.ZoneITU = resolveZoneByState(dxccStr, contact.AdministrativePrimary, false)
		}
	}

	// Infer grid from lat/lon
	if contact.Grid == 0 {
		latField, latOk := record.Get(spec.MyLatField.Name)
		lonField, lonOk := record.Get(spec.MyLonField.Name)
		if latOk && lonOk && latField.Value != "" && lonField.Value != "" {
			lat, latErr := parseADIFLocation(latField.Value)
			lon, lonErr := parseADIFLocation(lonField.Value)
			if latErr == nil && lonErr == nil {
				grid := latLonToMaidenhead(lat, lon)
				contact.Grid = maidenheadToNumber(grid)
				contact.GridSize = len(grid)
			}
		}
	}

	return contact, err
}
