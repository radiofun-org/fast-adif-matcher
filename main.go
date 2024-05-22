package main

import (
	"flag"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/skrushinsky/scaliger/julian"
	"log"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Theory of operation:
//
// Open ADIF file
// Calculate time extents of the ADIF file, e.g. first and last record
// Query the sqlite database where toId=uploader and time range between min/max extent
// Find matches and insert those matches into the matches database
// Insert each record from the uploaded ADIF into the sqlite database (so future queries can be made against them)

const MaxUint = ^uint(0)
const MinUint = 0

const MaxInt = int(^uint(0) >> 1)
const MinInt = -MaxInt - 1

var MODES = map[string]int{
	// Top 12 in usage order based on Club Log data: https://clublog.org/dxreport.html
	"FT8":          0,
	"CW":           1,
	"SSB":          2,
	"MFSK":         3,
	"FM":           4,
	"PSK":          5,
	"PKT":          6,
	"DIGITALVOICE": 7,
	"RTTY":         8,
	"MSK144":       9,
	"DYNAMIC":      10,
	"PAC":          11,

	// The rest in alphabetical order
	"AM":       12,
	"AMTORFEC": 13,
	"ARDOP":    14,
	"ASCI":     15,
	"ATV":      16,
	"C4FM":     17,
	"CHIP":     18,
	"CHIP128":  19,
	"CHIP64":   20,
	"CLO":      21,
	"CONTESTI": 22,
	"DOMINO":   23,
	"DOMINOF":  24,
	"DSTAR":    25,
	"FAX":      26,
	"FMHELL":   27,
	"FSK31":    28,
	"FSK441":   29,
	"GTOR":     30,
	"HELL":     31,
	"HELL80":   32,
	"HFSK":     33,
	"ISCAT":    34,
	"JT4":      35,
	"JT44":     36,
	"JT4A":     37,
	"JT4B":     38,
	"JT4C":     39,
	"JT4D":     40,
	"JT4E":     41,
	"JT4F":     42,
	"JT4G":     43,
	"JT65":     44,
	"JT65A":    45,
	"JT65B":    46,
	"JT65C":    47,
	"JT6M":     48,
	"JT9":      49,
	"MFSK16":   50,
	"MFSK8":    51,
	"MT63":     52,
	"OLIVIA":   53,
	"OPERA":    54,
	"PAC2":     55,
	"PAC3":     56,
	"PAX":      57,
	"PAX2":     58,
	"PCW":      59,
	"PSK10":    60,
	"PSK125":   61,
	"PSK2K":    62,
	"PSK31":    63,
	"PSK63":    64,
	"PSK63F":   65,
	"PSKAM10":  66,
	"PSKAM31":  67,
	"PSKAM50":  68,
	"PSKFEC31": 69,
	"PSKHELL":  70,
	"Q15":      71,
	"QPSK125":  72,
	"QPSK31":   73,
	"QPSK63":   74,
	"QRA64":    75,
	"ROS":      76,
	"RTTYM":    77,
	"SSTV":     78,
	"T10":      79,
	"THOR":     80,
	"THRB":     81,
	"THRBX":    82,
	"TOR":      83,
	"V4":       84,
	"VOI":      85,
	"WINMOR":   86,
	"WSPR":     87,
}

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

var START_JULIAN = julian.CivilToJulian(julian.CivilDate{Year: 1950, Month: 1, Day: 1})

func initDatabase(conn *sqlite.Conn) error {
	err := sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS contacts (
			id integer PRIMARY KEY,
			date integer,     -- 2 bytes,    julian date difference from 1900-01-01
			time integer,     -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			from_id integer,  -- 1-3 bytes,  the ID of the callsign record
			to_id integer,    -- 1-3 bytes,  the ID of the callsign record
			band integer,     -- 1 byte,     an enum into the ADIF BANDS list
			mode integer,     -- 1 byte,     an enum into the ADIF MODES list
			grid integer,     -- 1-4 bytes,  0 to 18,662,400
			grid_size integer -- 1 byte,     4 or 6
		)`, &sqlitex.ExecOptions{})
	if err != nil {
		return err
	}

	err = sqlitex.Execute(conn, `
		CREATE TABLE IF NOT EXISTS matches (
			id integer PRIMARY KEY,
			date integer,          -- 2 bytes,    julian date difference from 1900-01-01
			time integer,          -- 1-2 bytes,  minutes since midnight, e.g. 0-1439
			from_id integer,       -- 1-3 bytes,  the ID of the callsign record
			to_id integer,         -- 1-3 bytes,  the ID of the callsign record
			band integer,          -- 1 byte,     an enum into the ADIF BANDS list
			mode integer,          -- 1 byte,     an enum into the ADIF MODES list
			to_grid integer,       -- 1-4 bytes,  0 to 18,662,400
			to_grid_size integer,  -- 1 byte,     4 or 6
			from_grid integer,     -- 1-4 bytes,  0 to 18,662,400
			from_grid_size integer -- 1 byte,     4 or 6
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
	FromID   int
	ToID     int
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
			contact.FromID = stmt.ColumnInt(3)
			contact.ToID = stmt.ColumnInt(4)
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

	return int(julian.CivilToJulian(julian.CivilDate{Year: dateYear, Month: dateMonth, Day: dateDay}) - START_JULIAN), nil
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

		min, err := strconv.ParseFloat(bb.LowerFreqMhz, 64)
		if err != nil {
			return false
		}

		max, err := strconv.ParseFloat(bb.UpperFreqMhz, 64)
		if err != nil {
			return false
		}

		if min <= freq && freq <= max {
			r.Set(adif.Field{Name: name, Value: bb.Band})
			return true
		}
	}

	return false
}

func process(filename string, callsign string) {
	start := time.Now()

	conn, err := sqlite.OpenConn("./contacts.sqlite", sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		panic(err)
	}
	defer func(conn *sqlite.Conn) {
		_ = conn.Close()
	}(conn)

	err = initDatabase(conn)
	if err != nil {
		log.Panicf("Error initializing database: %v\n", err)
	}

	io := adif.NewADIIO()

	f, err := os.Open(filename)
	if err != nil {
		panic(err)
	}

	log, err := io.Read(f)
	if err != nil {
		panic(err)
	}

	calls := mapset.NewSet[string]()
	calls.Add(callsign)

	for _, record := range log.Records {
		call, ok := record.Get("CALL")
		if ok {
			calls.Add(call.Value)
		}
	}

	var callsInFile = calls.ToSlice()

	var callsInDatabase = map[string]int64{}
	var parameters []string

	for _ = range callsInFile {
		parameters = append(parameters, "?")
	}
	var parameterString = strings.Join(parameters, ", ")

	parameterValues := make([]interface{}, len(callsInFile))
	for i, v := range callsInFile {
		parameterValues[i] = v
	}

	err = sqlitex.Execute(conn,
		fmt.Sprintf(`SELECT id, callsign FROM callsigns WHERE callsign IN (%s)`, parameterString),
		&sqlitex.ExecOptions{Args: parameterValues,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				callsInDatabase[stmt.ColumnText(1)] = stmt.ColumnInt64(0)
				return nil
			}})
	if err != nil {
		panic(err)
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

	txEnd = sqlitex.Transaction(conn)

	var fromId = callsInDatabase[callsign]

	var dateStart = MaxInt
	var dateEnd = MinInt

	// TODO benchmark creating ids rows array here and executing queries at the end
	for _, record := range log.Records {
		var gridId int
		var gridLength int

		var modeId int
		var bandId int

		var dateJulianDifference int
		var minutesFromMidnight int

		var toId int64

		call, ok := record.Get("CALL")
		if ok {
			toId, ok = callsInDatabase[call.Value]
			if !ok || toId == 0 {
				fmt.Printf("can't find call '%s'\n", call.Value)
			}
		} else {
			fmt.Printf("can't find CALL in record %s\n", record)
		}

		band, ok := record.Get("BAND")
		if ok {
			bandId, ok = BANDS[band.Value]
		} else {
			ok = inferBand(record, "BAND")
			if ok {
				band, ok = record.Get("BAND")
				if ok {
					bandId, ok = BANDS[band.Value]
				}
			}
		}

		date, ok := record.Get("QSO_DATE")
		if ok {
			dateJulianDifference, err = dateStringToJulianDifference(date.Value)
			if err != nil {
				fmt.Printf("Error parsing date value: %v\n", err)
			} else {
				if dateJulianDifference < dateStart {
					dateStart = dateJulianDifference
				}
				if dateJulianDifference > dateEnd {
					dateEnd = dateJulianDifference
				}
			}
		}

		grid, ok := record.Get("MY_GRIDSQUARE")
		if ok {
			gridLength = len(grid.Value)
			gridId = maidenheadToNumber(grid.Value)
		}

		mode, ok := record.Get("MODE")
		if ok {
			modeId, ok = MODES[mode.Value]
			if ok {
				//fmt.Printf("MODE: %s, %d\n", mode.Value, modeId)
			}
		}

		timeOn, ok := record.Get("TIME_ON")
		if ok {
			minutesFromMidnight, err = timeStringToMinutesFromMidnight(timeOn.Value)
			if err != nil {
				fmt.Printf("Error parsing timeOn value: %v\n", err)
			}
		}

		err := sqlitex.Execute(
			conn, `INSERT INTO
					contacts (date, time, from_id, to_id, band, mode, grid, grid_size)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{dateJulianDifference, minutesFromMidnight, fromId, toId, bandId, modeId, gridId, gridLength}})
		if err != nil {
			panic(err)
		}
	}

	er = error(nil)
	txEnd(&er)

	potentialMatches, err := findPotentialMatches(conn, fromId, dateStart-1, dateEnd+1)

	//for _, match := range potentialMatches {
	//	fmt.Printf("%#v\n", match)
	//}

	duration := time.Since(start)

	fmt.Printf("%-16s Processed %d contacts in %dms at %fms/contact, found %d potential matches\n", filename, len(log.Records), duration.Milliseconds(), float32(duration.Milliseconds())/float32(len(log.Records)), len(potentialMatches))
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

	if *profileCpu != "" {
		f, err := os.Create(*profileCpu)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		err = pprof.StartCPUProfile(f)
		if err != nil {
			panic(err)
		}
		defer pprof.StopCPUProfile()
	}

	process(*filename, *callsign)
}
