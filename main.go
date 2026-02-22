package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"runtime/pprof"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/getsentry/sentry-go"
	_ "github.com/joho/godotenv/autoload"
	"golang.org/x/exp/constraints"
)

// Theory of operation:
//
// 1. Read ADIF file
// 2. Insert the valid records into the contacts table
// 3. Calculate time extents of the ADIF file, e.g. dates/times of the first and last record
// 4. Query the sqlite database where toId=the callsign ID of the file and time range between min/max extent
// 5. Find matches and insert those matches into the matches table

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

const MaxInt = int(^uint(0) >> 1)
const MinInt = -MaxInt - 1

func Abs[T constraints.Integer](x T) T {
	if x < 0 {
		return -x
	}

	return x
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

func process(filename string, callsign string) {
	debug.SetGCPercent(-1)
	start := time.Now()

	db, err := openDatabase("./contacts.sqlite")
	if err != nil {
		log.Panic(err)
	}
	defer db.Close()

	err = initDatabase(db)
	if err != nil {
		log.Panicf("Error initializing database: %v\n", err)
	}

	adifIo := adif.NewADIIO()

	f, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

	// Hash the file in a single pass while the ADIF parser reads it
	hasher := sha256.New()
	reader := io.TeeReader(f, hasher)

	logfile, err := adifIo.Read(reader)
	if err != nil {
		log.Panic(err)
	}

	adifVersion, _ := logfile.Header.Get(spec.AdifVerField.Name)
	programId, _ := logfile.Header.Get(spec.ProgramidField.Name)
	programVersion, _ := logfile.Header.Get(spec.ProgramversionField.Name)

	adifMetadataId, err := insertAdifMetadata(
		db,
		time.Now().Unix(),
		hex.EncodeToString(hasher.Sum(nil)),
		adifVersion.Value,
		programId.Value,
		programVersion.Value,
	)
	if err != nil {
		log.Panic(err)
	}

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

	callsInFile := calls.ToSlice()

	callsInFileJson, err := json.Marshal(callsInFile)
	if err != nil {
		log.Panicf("Could not marshal calls JSON: %v\n", err)
	}

	callsInDatabase, err := queryCallsignsByJSON(db, callsInFileJson)
	if err != nil {
		log.Panic(err)
	}

	callsInDatabase, err = bulkInsertCallsigns(db, callsInFile, callsInDatabase)
	if err != nil {
		log.Panic(err)
	}

	fromId := callsInDatabase[callsign]

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
		}
		if contact.Date > dateEnd {
			dateEnd = contact.Date
		}

		contacts = append(contacts, contact)
	}

	err = bulkInsertContacts(db, contacts, adifMetadataId)
	if err != nil {
		log.Panic(err)
	}

	potentialMatches, err := findPotentialMatches(db, fromId, dateStart-1, dateEnd+1)
	if err != nil {
		log.Panic(err)
	}

	// Index file contacts by ToID for O(1) lookup during matching
	contactsByToID := map[int64][]Contact{}
	for _, c := range contacts {
		contactsByToID[c.ToID] = append(contactsByToID[c.ToID], c)
	}

	matchedAt := time.Now().Unix()
	var matches []matchRecord

	for _, dbRecord := range potentialMatches {
		candidates, ok := contactsByToID[dbRecord.FromID]
		if !ok {
			continue
		}

		for _, fileRecord := range candidates {
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

			matches = append(matches, matchRecord{
				MatchedAt: matchedAt,
				AID:       dbRecord.ID,
				BID:       fileRecord.ID,
				ACallID:   dbRecord.FromID,
				BCallID:   fileRecord.FromID,
			})
		}
	}

	err = bulkInsertMatches(db, matches)
	if err != nil {
		log.Panic(err)
	}

	duration := time.Since(start)

	fmt.Printf(
		"%-16s Processed %d contacts in %dms at %fms/contact, found %d matches, %fms/found contact\n",
		filename,
		len(logfile.Records),
		duration.Milliseconds(),
		float32(duration.Milliseconds())/float32(len(logfile.Records)),
		len(matches),
		float32(duration.Milliseconds())/float32(len(matches)),
	)
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
