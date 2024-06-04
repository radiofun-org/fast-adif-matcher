package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/getsentry/sentry-go"
	_ "github.com/joho/godotenv/autoload"
	"github.com/skrushinsky/scaliger/julian"
	"golang.org/x/exp/constraints"
	"io"
	"log"
	"os"
	"regexp"
	"runtime/pprof"
	"strconv"
	"time"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Theory of operation:
//
// 1. Read ADIF file
// 2. Insert the valid records into the contacts table
// 3. Calculate time extents of the ADIF file, e.g. dates/times of the first and last record
// 4. Query the sqlite database where toId=the callsign ID of the file and time range between min/max extent
// 5. Find matches and insert those matches into the matches table

//const MaxUint = ^uint(0)
//const MinUint = 0

const MaxInt = int(^uint(0) >> 1)
const MinInt = -MaxInt - 1

var Numeric = regexp.MustCompile(`^\d+`)

func Abs[T constraints.Integer](x T) T {
	if x < 0 {
		return -x
	}

	return x
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

// TODO handle up to 10 character grids, support MY_GRIDSQUARE_EXT
// TODO IOTA: MY_IOTA (or MY_SIG + MY_SIG_INFO?) (MY_IOTA_ISLAND_ID seems undocumented and unused)
// TODO POTA: MY_POTA_REF (or MY_SIG + MY_SIG_INFO)
// TODO SOTA: MY_SOTA_REF (or MY_SIG + MY_SIG_INFO?)
// TODO WWFF: MY_WWFF_REF (or MY_SIG + MY_SIG_INFO?)
// TODO handle OPERATOR, STATION_CALLSIGN, OWNER_CALLSIGN?
// TODO handle BAND_RX/FREQ_RX? in practice is it ever different than BAND?
// TODO infer anything from MY_LAT/MY_LON?
// TODO SAT_MODE/SAT_NAME?
// TODO RST_SENT/RST_RCVD?
// TODO TIME_OFF

func initDatabase(conn *sqlite.Conn) error {
	err := sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS contacts (
			id INTEGER PRIMARY KEY,
			adif_metadata_id INTEGER,      -- 1-4 bytes,  ID of the adif_metadata record
			date INTEGER,                  -- 2 bytes,    julian date difference from 1900-01-01
			time INTEGER,                  -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			time_off INTEGER,              -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			from_id INTEGER,               -- 1-3 bytes,  the ID of the callsign record
			to_id INTEGER,                 -- 1-3 bytes,  the ID of the callsign record
			band INTEGER,                  -- 1 byte,     an enum into the ADIF Bands list
			mode INTEGER,                  -- 1 byte,     an enum into the ADIF Modes list
			sub_mode INTEGER,              -- 1 byte,     an enum into the ADIF SubModes list
			grid INTEGER,                  -- 1-4 bytes,  0 to 18,662,400
			grid_size INTEGER,             -- 1 byte,     4 or 6
			administrative_primary TEXT,   -- 1-3 bytes,  state from ADIF, convert to enum eventually?
			administrative_secondary TEXT, -- 1-n bytes,  county from ADIF, convert to enum eventually?
			dxcc TEXT,                     -- 1-3 bytes,  country from ADIF, convert to enum eventually?
			zone_cq INTEGER,               -- 1 byte,     CQ zone
			zone_itu INTEGER,              -- 1 byte,     ITU zone
			tx_power INTEGER,              -- 1 byte,     the transmit power in watts
			UNIQUE(date, time, from_id, to_id)
		)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	err = sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS adif_metadata (
			id integer PRIMARY KEY,
			process_time INTEGER, -- upload date as a UNIX epoch
			file_hash TEXT,       -- the SHA-256 hash of the file to avoid duplicates
			adif_version TEXT,    -- the ADIF version
			program_id TEXT,      -- the program name
			program_version TEXT, -- the program version
			UNIQUE(file_hash) ON CONFLICT IGNORE
		)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	err = sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS matches (
			id integer PRIMARY KEY,
			matched_at INTEGER,      -- 4 bytes     UNIX epoch time
			a_id INTEGER,            -- 1-4 bytes   ID of the record in the contacts table
			b_id INTEGER,            -- 1-4 bytes   ID of the record in the contacts table
			a_call_id INTEGER,       -- 1-3 bytes,  the ID of the callsign record
			b_call_id INTEGER,       -- 1-3 bytes,  the ID of the callsign record
			UNIQUE (a_id, b_id) ON CONFLICT IGNORE
		)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	err = sqlitex.Execute(conn, `CREATE INDEX IF NOT EXISTS date_to_id_index ON contacts (date, to_id)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	err = sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS callsigns (
			id integer PRIMARY KEY,
			callsign text unique
		)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	return nil
}

func addCallsign(conn *sqlite.Conn, callsign string) (int64, error) {
	var id int64

	err := sqlitex.Execute(conn,
		`INSERT OR IGNORE INTO callsigns (callsign) VALUES (?) RETURNING id, callsign`,
		&sqlitex.ExecOptions{Args: []any{callsign}, ResultFunc: func(stmt *sqlite.Stmt) error {
			id = stmt.ColumnInt64(0)
			return nil
		}})

	return id, err
}

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
	DXCC                    string
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

func findPotentialMatches(conn *sqlite.Conn, fromId int64, dateStart int, dateEnd int) ([]Contact, error) {
	var contacts []Contact

	err := sqlitex.Execute(conn,
		`SELECT id, date, time, time_off, from_id, to_id, band, mode, grid, grid_size
			   FROM contacts
			   WHERE
                  to_id = ? AND
			      date >= ? AND
                  date <= ?`,
		&sqlitex.ExecOptions{Args: []any{fromId, dateStart, dateEnd}, ResultFunc: func(stmt *sqlite.Stmt) error {
			var contact Contact

			contact.ID = stmt.ColumnInt64(0)
			contact.Date = stmt.ColumnInt(1)
			contact.Time = stmt.ColumnInt(2)
			contact.TimeOff = stmt.ColumnInt(3)
			contact.FromID = stmt.ColumnInt64(4)
			contact.ToID = stmt.ColumnInt64(5)
			contact.Band = stmt.ColumnInt(6)
			contact.Mode = stmt.ColumnInt(7)
			contact.Grid = stmt.ColumnInt(8)
			contact.GridSize = stmt.ColumnInt(9)

			contacts = append(contacts, contact)

			return nil
		}})

	return contacts, err
}

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

	return int(julian.CivilToJulian(julian.CivilDate{Year: dateYear, Month: dateMonth, Day: dateDay}) - StartJulian), nil
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

// TODO handle TimeOff
func withinThirtyMinutes(a Contact, b Contact) bool {
	// must be within one day to be within 30 minutes
	if Abs(a.Date-b.Date) > 1 {
		return false
	}

	if a.Date < b.Date {
		return (1440-a.Time)+b.Time <= 30
	}

	if a.Date > b.Date {
		return (1440-b.Time)+a.Time <= 30
	}

	// same day
	return Abs(a.Time-b.Time) <= 30
}

func recordToContact(fromId int64, record *adif.Record, callsInDatabase map[string]int64) (Contact, error) {
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
		contact.Band, ok = Bands[band.Value]
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
	if ok && grid.Value != "" {
		contact.Grid = maidenheadToNumber(grid.Value)
		contact.GridSize = len(grid.Value)
		if contact.GridSize == 8 {
			contact.GridSize = 6
		}
	}

	mode, ok := record.Get(spec.ModeField.Name)
	if ok {
		contact.Mode, ok = Modes[mode.Value]
		if !ok {
			return contact, fmt.Errorf("missing mode: %s\n", mode.Value)
		}
	}

	subMode, ok := record.Get(spec.SubmodeField.Name)
	if ok {
		contact.SubMode, ok = SubModes[subMode.Value]
		if !ok {
			return contact, fmt.Errorf("missing submode: %s\n", subMode.Value)
		}
	}

	//mySig, ok := record.Get(spec.MySigField.Name)
	//if ok {
	//	mySigInfo, ok := record.Get(spec.MySigInfoField.Name)
	//	if ok {
	//		fmt.Printf("%s: %s\n", mySig.Value, mySigInfo.Value)
	//	}
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
			replacer, err := regexp.Compile(fmt.Sprintf(`^%s,\s*`, contact.AdministrativePrimary))
			if err != nil {
				return contact, fmt.Errorf("error parsing regular expression: %v\n", err)
			}
			contact.AdministrativeSecondary = replacer.ReplaceAllString(administrativeSecondary.Value, "")
		} else {
			contact.AdministrativeSecondary = administrativeSecondary.Value
		}

		if len(contact.AdministrativePrimary) == 2 &&
			len(contact.AdministrativeSecondary) >= 3 &&
			contact.AdministrativeSecondary[2] == ',' &&
			contact.AdministrativeSecondary[0:2] != contact.AdministrativePrimary {
			// e.g. if primary is "PA" and secondary is "NC,Crow Hill" then let's zero them both out
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
		contact.DXCC = dxcc.Value
	}

	//SignalReportReceived    int
	//SignalReportSent        int
	//POTA                    POTA
	//IOTA                    IOTA
	//SOTA                    SOTA
	//WWFF                    WWFF

	return contact, err
}

func process(filename string, callsign string) {
	start := time.Now()

	conn, err := sqlite.OpenConn("./contacts.sqlite", sqlite.OpenCreate|sqlite.OpenReadWrite|sqlite.OpenWAL)
	if err != nil {
		log.Panic(err)
	}
	defer func(conn *sqlite.Conn) {
		_ = conn.Close()
	}(conn)

	err = initDatabase(conn)
	if err != nil {
		log.Panicf("Error initializing database: %v\n", err)
	}

	adifIo := adif.NewADIIO()

	f, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

	logfile, err := adifIo.Read(f)
	if err != nil {
		log.Panic(err)
	}

	// region ADIF metadata
	hasher := sha256.New()

	_, err = f.Seek(0, 0)
	if err != nil {
		log.Panic(err)
	}

	_, err = io.Copy(hasher, f)
	if err != nil {
		log.Panic(err)
	}

	adifVersion, _ := logfile.Header.Get(spec.AdifVerField.Name)
	programId, _ := logfile.Header.Get(spec.ProgramidField.Name)
	programVersion, _ := logfile.Header.Get(spec.ProgramversionField.Name)

	var adifMetadataId int64

	err = sqlitex.Execute(
		conn, `INSERT INTO
					adif_metadata (process_time, file_hash, adif_version, program_id, program_version)
					VALUES (?, ?, ?, ?, ?)
					RETURNING id`,
		&sqlitex.ExecOptions{Args: []any{time.Now().Unix(), hex.EncodeToString(hasher.Sum(nil)), adifVersion.Value, programId.Value, programVersion.Value}, ResultFunc: func(stmt *sqlite.Stmt) error {
			adifMetadataId = stmt.ColumnInt64(0)
			return nil
		}})
	if err != nil {
		log.Panic(err)
	}
	// endregion

	calls := mapset.NewSet[string]()

	// Add the file's callsign first
	calls.Add(callsign)

	// Collect the unique calls from all the contacts in the file
	for _, record := range logfile.Records {
		call, ok := record.Get(spec.CallField.Name)
		if ok {
			calls.Add(call.Value)
		}
	}

	var callsInFile = calls.ToSlice()
	var callsInDatabase = map[string]int64{}

	callsInFileJson, err := json.Marshal(callsInFile)
	if err != nil {
		log.Panicf("Could not marshal calls JSON: %v\n", err)
	}

	err = sqlitex.Execute(conn,
		fmt.Sprintf(`SELECT id, callsign FROM callsigns WHERE callsign IN (SELECT value FROM json_each(?))`),
		&sqlitex.ExecOptions{Args: []any{callsInFileJson},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				callsInDatabase[stmt.ColumnText(1)] = stmt.ColumnInt64(0)
				return nil
			}})
	if err != nil {
		log.Panic(err)
	}

	txEnd := sqlitex.Transaction(conn)

	for _, call := range callsInFile {
		_, ok := callsInDatabase[call]
		if !ok {
			id, err := addCallsign(conn, call)
			if err != nil {
				fmt.Printf("error adding callsign: %s\n", err)
			} else {
				callsInDatabase[call] = id
			}
		}
	}

	var er = error(nil)
	txEnd(&er)

	var fromId = callsInDatabase[callsign]

	var dateStart = MaxInt
	var dateEnd = MinInt

	var contacts []Contact

	for _, record := range logfile.Records {
		contact, err := recordToContact(fromId, record, callsInDatabase)
		if err != nil {
			fmt.Printf("error converting record: %v\n", err)
			continue
		}

		if contact.Date < dateStart {
			dateStart = contact.Date
		} else if contact.Date > dateEnd {
			dateEnd = contact.Date
		}

		contacts = append(contacts, contact)
	}

	txEnd = sqlitex.Transaction(conn)

	for i := range contacts {
		err := sqlitex.Execute(
			conn, `INSERT INTO
				contacts (adif_metadata_id, date, time, time_off, from_id, to_id, band, mode, sub_mode, grid, grid_size, administrative_primary, administrative_secondary, dxcc, zone_cq, zone_itu, tx_power)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(date, time, from_id, to_id)
				DO UPDATE SET
					adif_metadata_id=excluded.adif_metadata_id,
					time_off=excluded.time_off,
					band=excluded.band,
					mode=excluded.mode,
					grid=excluded.grid,
					grid_size=excluded.grid_size,
					administrative_primary=excluded.administrative_primary,
					administrative_secondary=excluded.administrative_secondary,
					dxcc=excluded.dxcc,
					zone_cq=excluded.zone_cq,
					zone_itu=excluded.zone_itu,
					tx_power=excluded.tx_power
				RETURNING id`,
			&sqlitex.ExecOptions{Args: []any{
				adifMetadataId,
				contacts[i].Date,
				contacts[i].Time,
				contacts[i].TimeOff,
				contacts[i].FromID,
				contacts[i].ToID,
				contacts[i].Band,
				contacts[i].Mode,
				contacts[i].SubMode,
				contacts[i].Grid,
				contacts[i].GridSize,
				contacts[i].AdministrativePrimary,
				contacts[i].AdministrativeSecondary,
				contacts[i].DXCC,
				contacts[i].ZoneCQ,
				contacts[i].ZoneITU,
				contacts[i].TransmitPower,
			}, ResultFunc: func(stmt *sqlite.Stmt) error {
				contacts[i].ID = stmt.ColumnInt64(0)
				return nil
			}})
		if err != nil {
			log.Panic(err)
		}
	}

	er = error(nil)
	txEnd(&er)

	potentialMatches, err := findPotentialMatches(conn, fromId, dateStart-1, dateEnd+1)
	if err != nil {
		log.Panic(err)
	}

	var matchesFound = 0

	txEnd = sqlitex.Transaction(conn)

	for _, dbRecord := range potentialMatches {
		for _, fileRecord := range contacts {
			if dbRecord.FromID != fileRecord.ToID {
				continue
			}

			if dbRecord.Band != fileRecord.Band {
				continue
			}

			if !withinThirtyMinutes(dbRecord, fileRecord) {
				continue
			}

			if dbRecord.Mode != fileRecord.Mode {
				modeGroupFrom, ok := ModeGroupsById[dbRecord.Mode]
				if !ok {
					fmt.Printf("Missing mode group: %v\n", dbRecord.Mode)
					continue
				}

				modeGroupTo, ok := ModeGroupsById[fileRecord.Mode]
				if !ok {
					fmt.Printf("Missing mode group: %v\n", fileRecord.Mode)
					continue
				}

				if modeGroupFrom != modeGroupTo {
					continue
				}
			}

			err := sqlitex.Execute(
				conn, `INSERT INTO
					matches (matched_at, a_id, b_id, a_call_id, b_call_id)
					VALUES (?, ?, ?, ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{time.Now().Unix(), dbRecord.ID, fileRecord.ID, dbRecord.FromID, fileRecord.FromID}})
			if err != nil {
				log.Panic(err)
			}

			matchesFound++
		}
	}

	er = error(nil)
	txEnd(&er)

	duration := time.Since(start)

	fmt.Printf("%-16s Processed %d contacts in %dms at %fms/contact, found %d matches, %fms/found contact\n", filename, len(logfile.Records), duration.Milliseconds(), float32(duration.Milliseconds())/float32(len(logfile.Records)), matchesFound, float32(duration.Milliseconds())/float32(matchesFound))
}

var filename = flag.String("filename", "", "the filename to import")
var callsign = flag.String("callsign", "", "the callsign for the file in question")

var profileCpu = flag.String("profile-cpu", "", "write cpu profile to `file`")

// TODO create resident version that accepts uploads and enqueues them for service by goroutines

func main() {
	sentryDsn := os.Getenv("SENTRY_DSN")

	if sentryDsn != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              sentryDsn,
			TracesSampleRate: 0.0,
			Debug:            false,
		})
		if err != nil {
			log.Fatalf("sentry.Init: %s", err)
		}
		defer sentry.Flush(2 * time.Second)
	}

	flag.Parse()

	if filename == nil || *filename == "" || callsign == nil || *callsign == "" {
		flag.Usage()
		os.Exit(1)
	}

	for k, v := range ModeGroups {
		modeId, ok := Modes[k]
		if !ok {
			fmt.Printf("Can't find mode '%s'\n", k)
			continue
		}
		ModeGroupsById[modeId] = v
	}

	if *profileCpu != "" {
		f, err := os.Create(*profileCpu)
		if err != nil {
			log.Panic(err)
		}
		defer func(f *os.File) { _ = f.Close() }(f)

		err = pprof.StartCPUProfile(f)
		if err != nil {
			log.Panic(err)
		}
		defer pprof.StopCPUProfile()
	}

	process(*filename, *callsign)
}
