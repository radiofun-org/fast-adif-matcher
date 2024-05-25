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
	"github.com/skrushinsky/scaliger/julian"
	"golang.org/x/exp/constraints"
	"io"
	"log"
	"os"
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

func Abs[T constraints.Integer](x T) T {
	if x < 0 {
		return -x
	}

	return x
}

var MODES = map[string]int{
	// TODO renumber these
	"AM":           12,
	"AMTOR":        101,
	"AMTORFEC":     13,
	"ARDOP":        14,
	"ASCI":         15,
	"ATV":          16,
	"C4FM":         17,
	"CHIP":         18,
	"CHIP128":      19,
	"CHIP64":       20,
	"CLO":          21,
	"CLOVER":       91, // not an actual ADIF mode
	"CONTESTI":     22,
	"CW":           1,
	"DATA":         92, // not an actual ADIF mode
	"DIGITALVOICE": 7,
	"DOMINO":       23,
	"DOMINOF":      24,
	"DMR":          100,
	"DSTAR":        25,
	"DYNAMIC":      10,
	"FAX":          26,
	"FM":           4,
	"FMHELL":       27,
	"FSK31":        28,
	"FSK441":       29,
	"FST4":         89, // not an actual ADIF mode
	"FT4":          96, // not an actual ADIF mode
	"FT8":          0,
	"GTOR":         30,
	"HELL":         31,
	"HELL80":       32,
	"HFSK":         33,
	"IMAGE":        97, // not an actual ADIF mode
	"ISCAT":        34,
	"JT4":          35,
	"JT44":         36,
	"JT4A":         37,
	"JT4B":         38,
	"JT4C":         39,
	"JT4D":         40,
	"JT4E":         41,
	"JT4F":         42,
	"JT4G":         43,
	"JT65":         44,
	"JT65A":        45,
	"JT65B":        46,
	"JT65C":        47,
	"JT6M":         48,
	"JT9":          49,
	"MFSK":         3,
	"MFSK16":       50,
	"MFSK8":        51,
	"MSK144":       9,
	"MT63":         52,
	"OLIVIA":       53,
	"OPERA":        54,
	"PAC":          11,
	"PAC2":         55,
	"PAC3":         56,
	"PACKET":       93, // not an actual ADIF mode
	"PACTOR":       95, // not an actual ADIF mode
	"PAX":          57,
	"PAX2":         58,
	"PCW":          59,
	"PHONE":        94, // not an actual ADIF mode
	"PKT":          6,
	"PSK":          5,
	"PSK10":        60,
	"PSK125":       61,
	"PSK2K":        62,
	"PSK31":        63,
	"PSK63":        64,
	"PSK63F":       65,
	"PSKAM":        90, // not an actual ADIF mode
	"PSKAM10":      66,
	"PSKAM31":      67,
	"PSKAM50":      68,
	"PSKFEC31":     69,
	"PSKHELL":      70,
	"Q15":          71,
	"Q65":          98, // not an actual ADIF mode
	"QPSK125":      72,
	"QPSK31":       73,
	"QPSK63":       74,
	"QRA64":        75,
	"ROS":          76,
	"RTTY":         8,
	"RTTYM":        77,
	"SSB":          2,
	"SSTV":         78,
	"T10":          79,
	"THOR":         80,
	"THRB":         81,
	"THRBX":        82,
	"THROB":        99, // not an actual ADIF mode
	"TOR":          83,
	"V4":           84,
	"VOI":          85,
	"WINMOR":       86,
	"WSPR":         87,
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

var BANDS = map[string]int{
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

// TODO state (primary administrative)
// TODO county (secondary administrative)
// TODO country
// TODO CQ zone
// TODO ITU zone
// TODO IOTA

func initDatabase(conn *sqlite.Conn) error {
	// TODO add metadata table for ADIF metadata

	err := sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS contacts (
			id integer PRIMARY KEY,
			date integer,      -- 2 bytes,    julian date difference from 1900-01-01
			time integer,      -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			from_id integer,   -- 1-3 bytes,  the ID of the callsign record
			to_id integer,     -- 1-3 bytes,  the ID of the callsign record
			band integer,      -- 1 byte,     an enum into the ADIF BANDS list
			mode integer,      -- 1 byte,     an enum into the ADIF MODES list
			grid integer,      -- 1-4 bytes,  0 to 18,662,400
			grid_size integer, -- 1 byte,     4 or 6
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

	// TODO Should match table only contain the ID pairings/times/call IDs? Then we can update the individual files with those...
	err = sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS matches (
			id integer PRIMARY KEY,
			created integer,         -- 4 bytes     UNIX epoch time
			a_id integer,            -- 1-4 bytes   ID of the record in the contacts table
			b_id integer,            -- 1-4 bytes   ID of the record in the contacts table
			a_date integer,          -- 2 bytes,    julian date difference from 1950-01-01
			a_time integer,          -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			b_date integer,          -- 2 bytes,    julian date difference from 1950-01-01
			b_time integer,          -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			a_call_id integer,       -- 1-3 bytes,  the ID of the callsign record
			b_call_id integer,       -- 1-3 bytes,  the ID of the callsign record
			a_band integer,          -- 1 byte,     an enum into the ADIF BANDS list
			a_mode integer,          -- 1 byte,     an enum into the ADIF MODES list
			b_band integer,          -- 1 byte,     an enum into the ADIF BANDS list
			b_mode integer,          -- 1 byte,     an enum into the ADIF MODES list
			a_grid integer,          -- 1-4 bytes,  0 to 18,662,400
			a_grid_size integer,     -- 1 byte,     4 or 6
			b_grid integer,          -- 1-4 bytes,  0 to 18,662,400
			b_grid_size integer,     -- 1 byte,     4 or 6
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

type Contact struct {
	ID       int64
	Date     int
	Time     int
	FromID   int64
	ToID     int64
	Band     int
	Mode     int
	Grid     int
	GridSize int
}

func findPotentialMatches(conn *sqlite.Conn, fromId int64, dateStart int, dateEnd int) ([]Contact, error) {
	var contacts []Contact

	err := sqlitex.Execute(conn,
		`SELECT id, date, time, from_id, to_id, band, mode, grid, grid_size
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
			contact.FromID = stmt.ColumnInt64(3)
			contact.ToID = stmt.ColumnInt64(4)
			contact.Band = stmt.ColumnInt(5)
			contact.Mode = stmt.ColumnInt(6)
			contact.Grid = stmt.ColumnInt(7)
			contact.GridSize = stmt.ColumnInt(8)

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

	adifVersion, _ := logfile.Header.Get("ADIF_VER")
	programId, _ := logfile.Header.Get("PROGRAMID")
	programVersion, _ := logfile.Header.Get("PROGRAMVERSION")

	err = sqlitex.Execute(
		conn, `INSERT INTO
					adif_metadata (process_time, file_hash, adif_version, program_id, program_version)
					VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{time.Now().Unix(), hex.EncodeToString(hasher.Sum(nil)), adifVersion.Value, programId.Value, programVersion.Value}})
	if err != nil {
		log.Panic(err)
	}
	// endregion

	calls := mapset.NewSet[string]()

	// Add the file's callsign first
	calls.Add(callsign)

	// Collect the unique calls from all the contacts in the file
	for _, record := range logfile.Records {
		call, ok := record.Get("CALL")
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
				contacts (date, time, from_id, to_id, band, mode, grid, grid_size)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(date, time, from_id, to_id)
				DO UPDATE SET
					band=excluded.band,
					mode=excluded.mode,
					grid=excluded.grid,
					grid_size=excluded.grid_size
				RETURNING id`,
			&sqlitex.ExecOptions{Args: []any{contacts[i].Date, contacts[i].Time, contacts[i].FromID, contacts[i].ToID, contacts[i].Band, contacts[i].Mode, contacts[i].Grid, contacts[i].GridSize}, ResultFunc: func(stmt *sqlite.Stmt) error {
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
					matches (created, a_id, b_id, a_date, a_time, b_date, b_time, a_call_id, b_call_id, a_band, a_mode, b_band, b_mode, a_grid, a_grid_size, b_grid, b_grid_size)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{time.Now().Unix(), dbRecord.ID, fileRecord.ID, dbRecord.Date, dbRecord.Time, fileRecord.Date, fileRecord.Time, dbRecord.FromID, fileRecord.FromID, dbRecord.Band, dbRecord.Mode, fileRecord.Band, fileRecord.Mode, dbRecord.Grid, dbRecord.GridSize, fileRecord.Grid, fileRecord.GridSize}})
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

func recordToContact(fromId int64, record *adif.Record, callsInDatabase map[string]int64) (Contact, error) {
	contact := Contact{
		FromID: fromId,
	}

	var err error

	call, ok := record.Get("CALL")
	if ok {
		contact.ToID, ok = callsInDatabase[call.Value]
		if !ok || contact.ToID == 0 {
			return contact, fmt.Errorf("can't find call '%s'\n", call.Value)
		}
	} else {
		return contact, fmt.Errorf("can't find CALL in record %s\n", record)
	}

	band, ok := record.Get("BAND")
	if ok {
		contact.Band, ok = BANDS[band.Value]
	} else {
		ok = inferBand(record, "BAND")
		if ok {
			band, ok = record.Get("BAND")
			if ok {
				contact.Band, ok = BANDS[band.Value]
			}
		}
	}

	date, ok := record.Get("QSO_DATE")
	if ok {
		contact.Date, err = dateStringToJulianDifference(date.Value)
		if err != nil {
			return contact, fmt.Errorf("Error parsing date value: %v\n", err)
		}
	}

	grid, ok := record.Get("MY_GRIDSQUARE")
	if ok && grid.Value != "" {
		contact.Grid = maidenheadToNumber(grid.Value)
		contact.GridSize = len(grid.Value)
		if contact.GridSize == 8 {
			contact.GridSize = 6
		}
	}

	mode, ok := record.Get("MODE")
	if ok {
		contact.Mode, ok = MODES[mode.Value]
		if !ok {
			return contact, fmt.Errorf("missing mode: %s\n", mode.Value)
		}
	}

	timeOn, ok := record.Get("TIME_ON")
	if ok {
		contact.Time, err = timeStringToMinutesFromMidnight(timeOn.Value)
		if err != nil {
			return contact, fmt.Errorf("error parsing timeOn value: %v\n", err)
		}
	}

	return contact, err
}

var filename = flag.String("filename", "", "the filename to import")
var callsign = flag.String("callsign", "", "the callsign for the file in question")

var profileCpu = flag.String("profile-cpu", "", "write cpu profile to `file`")

// TODO create resident version that accepts uploads and enqueues them for service by goroutines

func main() {
	flag.Parse()

	if filename == nil || *filename == "" || callsign == nil || *callsign == "" {
		flag.Usage()
		os.Exit(1)
	}

	for k, v := range ModeGroups {
		modeId, ok := MODES[k]
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
