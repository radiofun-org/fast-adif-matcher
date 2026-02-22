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

type WWFF struct {
	Program int
	Number  int
}

type POTA struct {
	Country   int
	Reference int
}

type IOTA struct {
	Continent  int
	Designator int
}

type SOTA struct {
	ITU         int
	Subdivision int
	Reference   int
}

type Contact struct {
	ID                      int64
	Date                    int
	Time                    int
	TimeOff                 int
	FromID                  int64
	ToID                    int64
	Band                    int
	Mode                    int
	SubMode                 int
	Grid                    int
	GridSize                int
	ZoneCQ                  int
	ZoneITU                 int
	DXCC                    int
	AdministrativePrimary   string
	AdministrativeSecondary string
	SignalReportReceived    int
	SignalReportSent        int
	TransmitPower           int
	POTA                    POTA
	IOTA                    IOTA
	SOTA                    SOTA
	WWFF                    WWFF
}

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

var ModeGroupsById = map[int]string{}

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

var StartJulian = julian.CivilToJulian(julian.CivilDate{Year: 1950, Month: 1, Day: 1})

var Numeric = regexp.MustCompile(`^\d+`)

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

func inferBand(r *adif.Record, name string) bool {
	freqname := spec.FreqField.Name

	if name == spec.BandRxField.Name {
		freqname = spec.FreqRxField.Name
	}

	f, ok := r.Get(freqname)
	if !ok || f.Value == "" {
		return false
	}

	freq, err := strconv.ParseFloat(f.Value, 64)
	if err != nil {
		return false
	}

	for _, b := range spec.BandEnumeration.Values {
		bb := b.(spec.BandEnum)

		_min, err := strconv.ParseFloat(bb.LowerFreqMhz, 64)
		if err != nil {
			return false
		}

		_max, err := strconv.ParseFloat(bb.UpperFreqMhz, 64)
		if err != nil {
			return false
		}

		if _min <= freq && freq <= _max {
			_ = r.Set(adif.Field{Name: name, Value: bb.Band})
			return true
		}
	}

	return false
}

func recordToContact(
	fromId int64,
	record *adif.Record,
	callsInDatabase map[string]int64,
) (Contact, error) {
	contact := Contact{
		FromID: fromId,
	}

	var err error

	call, ok := record.Get(spec.CallField.Name)
	if ok {
		contact.ToID, ok = callsInDatabase[call.Value]
		if !ok || contact.ToID == 0 {
			return contact, fmt.Errorf("can't find call '%s'\n", call.Value)
		}
	} else {
		return contact, fmt.Errorf("can't find CALL in record %s\n", record)
	}

	band, ok := record.Get(spec.BandField.Name)
	if ok {
		contact.Band, ok = Bands[strings.ToLower(band.Value)]
	} else {
		ok = inferBand(record, spec.BandField.Name)
		if ok {
			band, ok = record.Get(spec.BandField.Name)
			if ok {
				contact.Band, ok = Bands[band.Value]
			}
		}
	}

	date, ok := record.Get(spec.QsoDateField.Name)
	if ok {
		contact.Date, err = dateStringToJulianDifference(date.Value)
		if err != nil {
			return contact, fmt.Errorf("Error parsing date value: %v\n", err)
		}
	}

	grid, ok := record.Get(spec.MyGridsquareField.Name)
	if ok {
		gridValue := grid.Value
		gridExt, hasExt := record.Get(spec.MyGridsquareExtField.Name)
		if hasExt && gridExt.Value != "" {
			gridValue += gridExt.Value
		}
		gridLen := len(gridValue)
		if gridLen >= 4 && gridLen <= 12 && gridLen%2 == 0 {
			contact.Grid = maidenheadToNumber(gridValue)
			contact.GridSize = gridLen
		}
	}

	mode, ok := record.Get(spec.ModeField.Name)
	if ok {
		contact.Mode, ok = Modes[strings.ToUpper(mode.Value)]
		if !ok {
			return contact, fmt.Errorf("missing mode: %s\n", mode.Value)
		}
	}

	subMode, ok := record.Get(spec.SubmodeField.Name)
	if ok {
		contact.SubMode, ok = SubModes[strings.ToUpper(subMode.Value)]
		if !ok {
			return contact, fmt.Errorf("missing submode: %s\n", subMode.Value)
		}
	}

	//mySig, ok := record.Get(spec.MySigField.Name)
	//if ok {
	//        mySigInfo, ok := record.Get(spec.MySigInfoField.Name)
	//        if ok {
	//                fmt.Printf("%s: %s\n", mySig.Value, mySigInfo.Value)
	//        }
	//}

	timeOn, ok := record.Get(spec.TimeOnField.Name)
	if ok {
		contact.Time, err = timeStringToMinutesFromMidnight(timeOn.Value)
		if err != nil {
			return contact, fmt.Errorf("error parsing timeOn value: %v\n", err)
		}
	}

	zoneCq, ok := record.Get(spec.MyCqZoneField.Name)
	if ok && zoneCq.Value != "" {
		contact.ZoneCQ, err = strconv.Atoi(zoneCq.Value)
		if err != nil {
			return contact, fmt.Errorf("error parsing zoneCq value: %v\n", err)
		}
	}

	zoneItu, ok := record.Get(spec.MyItuZoneField.Name)
	if ok && zoneItu.Value != "" {
		contact.ZoneITU, err = strconv.Atoi(zoneItu.Value)
		if err != nil {
			return contact, fmt.Errorf("error parsing zoneItu value: %v\n", err)
		}
	}

	administrativePrimary, ok := record.Get(spec.MyStateField.Name)
	if ok && administrativePrimary.Value != "" {
		contact.AdministrativePrimary = administrativePrimary.Value
	}

	administrativeSecondary, ok := record.Get(spec.MyCntyField.Name)
	if ok && administrativeSecondary.Value != "" && administrativeSecondary.Value != "USA" {
		if contact.AdministrativePrimary != "" {
			prefix := contact.AdministrativePrimary + ","
			if strings.HasPrefix(administrativeSecondary.Value, prefix) {
				contact.AdministrativeSecondary = strings.TrimLeft(
					administrativeSecondary.Value[len(prefix):],
					" ",
				)
			} else {
				contact.AdministrativeSecondary = administrativeSecondary.Value
			}
		} else {
			contact.AdministrativeSecondary = administrativeSecondary.Value
		}

		if len(contact.AdministrativePrimary) == 2 &&
			len(contact.AdministrativeSecondary) >= 3 &&
			contact.AdministrativeSecondary[2] == ',' &&
			contact.AdministrativeSecondary[0:2] != contact.AdministrativePrimary {
			contact.AdministrativePrimary = ""
			contact.AdministrativeSecondary = ""
		}
	}

	transmitPower, ok := record.Get(spec.TxPwrField.Name)
	if ok && transmitPower.Value != "" {
		match := Numeric.FindString(transmitPower.Value)
		if match != "" {
			contact.TransmitPower, err = strconv.Atoi(match)
			if err != nil {
				return contact, fmt.Errorf("error parsing transmitPower value: %v\n", err)
			}
		}
	}

	dxcc, ok := record.Get(spec.DxccField.Name)
	if ok && dxcc.Value != "" {
		contact.DXCC, err = strconv.Atoi(dxcc.Value)
		if err != nil {
			return contact, fmt.Errorf("error parsing dxcc value: %v\n", err)
		}
	}

	//SignalReportReceived    int
	//SignalReportSent        int
	//POTA                    POTA
	//IOTA                    IOTA
	//SOTA                    SOTA
	//WWFF                    WWFF

	return contact, err
}
