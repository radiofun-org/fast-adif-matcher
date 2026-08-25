package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/joho/godotenv/autoload"
	"github.com/redis/go-redis/v9"
)

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// normalizeCallsign canonicalizes a callsign for storage and lookup.
// ADIF files vary in case and padding; without this, "w1aw" and "W1AW"
// would be distinct callsigns that never match each other.
func normalizeCallsign(callsign string) string {
	return strings.ToUpper(strings.TrimSpace(callsign))
}

// contactEnd returns the absolute end time in minutes for a contact.
// If TimeOff is set and less than Time, the QSO crossed midnight.
// If TimeOff is not set (0), the contact is treated as a single point.
func contactEnd(c Contact) int {
	end := c.Date*1440 + c.Time
	if c.TimeOff > 0 {
		end = c.Date*1440 + c.TimeOff
		if c.TimeOff < c.Time {
			end += 1440 // crossed midnight
		}
	}
	return end
}

// withinThirtyMinutes checks whether two contacts' time ranges overlap or
// are within 30 minutes of each other. Each contact spans [Time, TimeOff];
// if TimeOff is 0 it's treated as a point at Time.
func withinThirtyMinutes(a Contact, b Contact) bool {
	if abs(a.Date-b.Date) > 1 {
		return false
	}
	aStart := a.Date*1440 + a.Time
	aEnd := contactEnd(a)
	bStart := b.Date*1440 + b.Time
	bEnd := contactEnd(b)

	// Gap between two intervals; 0 if they overlap.
	gap := max(aStart, bStart) - min(aEnd, bEnd)
	return gap <= 30
}

// initModeMaps populates the mode lookup tables used during matching and
// record conversion.
func initModeMaps() {
	for k, v := range ModeGroups {
		modeId, ok := Modes[k]
		if !ok {
			fmt.Printf("Can't find mode '%s'\n", k)
			continue
		}
		ModeGroupsById[modeId] = v
	}

	// Build submodeToMode by iterating the spec's submode enumeration.
	// Maps e.g. "USB" → "SSB", "FT4" → "MFSK".
	for _, v := range spec.SubmodeEnumeration.Values {
		sub := v.(spec.SubmodeEnum)
		submodeToMode[strings.ToUpper(sub.Submode)] = strings.ToUpper(sub.Mode)
	}
}

// contactsMatch reports whether two contacts describe the same QSO: same
// band, within the 30-minute window, and same mode or mode group (e.g. FT8
// and FT4 are both DATA). Band 0 means the band was missing and couldn't be
// inferred; two unknowns are not evidence of the same band, so they never
// match.
func contactsMatch(a Contact, b Contact) bool {
	if a.Band == 0 || a.Band != b.Band {
		return false
	}
	if !withinThirtyMinutes(a, b) {
		return false
	}
	if a.Mode != b.Mode {
		aGroup, ok1 := ModeGroupsById[a.Mode]
		bGroup, ok2 := ModeGroupsById[b.Mode]
		if !ok1 {
			fmt.Printf("Missing mode group: %v\n", a.Mode)
			return false
		}
		if !ok2 {
			fmt.Printf("Missing mode group: %v\n", b.Mode)
			return false
		}
		if aGroup != bGroup {
			return false
		}
	}
	return true
}

func readerForFilename(filename string) adif.Reader {
	if strings.EqualFold(filepath.Ext(filename), ".adx") {
		return adif.NewADXIO()
	}
	return adif.NewADIIO()
}

// staleContactKeys parses the superseded log and returns the identity keys it
// asserted that the new upload no longer does. Those contacts had a wrong
// identity field (callsign, date, or time) — a corrected record has a new
// identity key, so the upsert can't touch the old row and it must be deleted
// explicitly.
func staleContactKeys(
	ctx context.Context,
	pool *pgxpool.Pool,
	oldFilename string,
	fromId int64,
	newContacts []Contact,
) ([]contactKey, error) {
	f, err := os.Open(oldFilename)
	if err != nil {
		return nil, err
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	logfile, err := readerForFilename(oldFilename).Read(f)
	if err != nil {
		return nil, err
	}

	calls := mapset.NewSet[string]()
	for _, record := range logfile.Records {
		if call, ok := record.Get(spec.CallField.Name); ok {
			calls.Add(normalizeCallsign(call.Value))
		}
	}
	callsInDB, err := queryCallsigns(ctx, pool, calls.ToSlice())
	if err != nil {
		return nil, err
	}

	newKeys := map[contactKey]bool{}
	for _, c := range newContacts {
		newKeys[contactKey{ToID: c.ToID, Date: c.Date, Time: c.Time}] = true
	}

	var stale []contactKey
	seen := map[contactKey]bool{}
	for _, record := range logfile.Records {
		// A callsign missing from the DB means the original processing never
		// stored this record, so there is nothing to delete.
		contact, err := recordToContact(fromId, record, callsInDB)
		if err != nil {
			continue
		}
		k := contactKey{ToID: contact.ToID, Date: contact.Date, Time: contact.Time}
		if !newKeys[k] && !seen[k] {
			seen[k] = true
			stale = append(stale, k)
		}
	}
	return stale, nil
}

// process is the core pipeline for an ADIF log file:
//  1. Parse the file and compute its SHA-256 (for dedup)
//  2. Ensure all callsigns exist in the database
//  3. Convert ADIF records to compact Contact structs and bulk-insert them
//  4. Re-check existing matches on the upserted contacts (a correction can
//     break a match) and, for a re-upload, delete contacts the superseded
//     file asserted but this one doesn't
//  5. Find matching QSOs — contacts already in the DB where the other station
//     logged the same QSO (same band, compatible mode, within 30 minutes)
//
// replacesFilename, when non-empty, is the path of the log this upload
// corrects ("re-upload this log"); empty means a plain incremental upload.
func process(
	ctx context.Context,
	pool *pgxpool.Pool,
	rdb *redis.Client,
	filename string,
	callsign string,
	replacesFilename string,
) {
	callsign = normalizeCallsign(callsign)
	start := time.Now()

	adifReader := readerForFilename(filename)

	f, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

	hasher := sha256.New()
	logfile, err := adifReader.Read(io.TeeReader(f, hasher))
	if err != nil {
		log.Panic(err)
	}

	adifVersion, _ := logfile.Header.Get(spec.AdifVerField.Name)
	programId, _ := logfile.Header.Get(spec.ProgramidField.Name)
	programVersion, _ := logfile.Header.Get(spec.ProgramversionField.Name)

	adifMetadataId, err := insertAdifMetadata(
		ctx, pool,
		time.Now().Unix(),
		hex.EncodeToString(hasher.Sum(nil)),
		adifVersion.Value,
		programId.Value,
		programVersion.Value,
	)
	if err != nil {
		log.Panic(err)
	}

	// Collect all unique callsigns from the file and ensure they exist in the DB.
	calls := mapset.NewSet[string]()
	calls.Add(callsign)
	for _, record := range logfile.Records {
		if call, ok := record.Get(spec.CallField.Name); ok {
			calls.Add(normalizeCallsign(call.Value))
		}
	}

	callsInFile := calls.ToSlice()

	callsInDB, err := queryCallsigns(ctx, pool, callsInFile)
	if err != nil {
		log.Panic(err)
	}
	callsInDB, err = bulkInsertCallsigns(ctx, pool, callsInFile, callsInDB)
	if err != nil {
		log.Panic(err)
	}

	fromId := callsInDB[callsign]

	// Convert ADIF records to Contact structs, tracking the date range
	// so we can scope the reverse-lookup query.
	dateStart := math.MaxInt
	dateEnd := math.MinInt
	var contacts []Contact

	for _, record := range logfile.Records {
		contact, err := recordToContact(fromId, record, callsInDB)
		if err != nil {
			fmt.Printf("error converting record: %v\n", err)
			continue
		}
		if contact.Date < dateStart {
			dateStart = contact.Date
		}
		endDate := contact.Date
		if contact.TimeOff > 0 && contact.TimeOff < contact.Time {
			endDate++ // TimeOff crosses midnight
		}
		if endDate > dateEnd {
			dateEnd = endDate
		}
		contacts = append(contacts, contact)
	}

	err = bulkInsertContacts(ctx, pool, contacts, adifMetadataId)
	if err != nil {
		log.Panic(err)
	}

	// Callsign IDs whose award progress must be re-evaluated: both sides of
	// any deleted match, plus both sides of any new match (added below).
	affected := mapset.NewSet[int64]()

	// A correction in this upload (band, mode, end time) can break a match
	// made under the old values.
	upsertedIDs := make([]int64, len(contacts))
	for i, c := range contacts {
		upsertedIDs[i] = c.ID
	}
	revalidationAffected, err := revalidateMatches(ctx, pool, upsertedIDs)
	if err != nil {
		log.Panic(err)
	}
	affected.Append(revalidationAffected...)

	// For a re-upload, contacts the superseded file asserted but this file
	// doesn't were corrections to identity fields: delete them and any
	// matches they made.
	if replacesFilename != "" {
		stale, err := staleContactKeys(ctx, pool, replacesFilename, fromId, contacts)
		if err != nil {
			log.Panic(err)
		}
		deletedIDs, err := deleteContactsByKeys(ctx, pool, fromId, stale)
		if err != nil {
			log.Panic(err)
		}
		if len(deletedIDs) > 0 {
			log.Printf("Re-upload deleted %d superseded contact(s)", len(deletedIDs))
			deletionAffected, err := deleteMatchesReferencing(ctx, pool, deletedIDs)
			if err != nil {
				log.Panic(err)
			}
			affected.Append(deletionAffected...)
		}
	}

	// Match against existing contacts in the DB. findPotentialMatches returns
	// contacts where someone else logged a QSO *to* this callsign in the same
	// date range. We index the file's contacts by ToID so we can efficiently
	// check each potential match against the right subset.
	// Pad by 1 day on each side so contacts near midnight or within the
	// 30-minute matching window on adjacent days aren't missed.
	potentialMatches, err := findPotentialMatches(ctx, pool, fromId, dateStart-1, dateEnd+1)
	if err != nil {
		log.Panic(err)
	}

	contactsByToID := map[int64][]Contact{}
	for _, c := range contacts {
		contactsByToID[c.ToID] = append(contactsByToID[c.ToID], c)
	}

	matchedAt := time.Now().Unix()
	var matches []matchRecord

	for _, dbContact := range potentialMatches {
		candidates, ok := contactsByToID[dbContact.FromID]
		if !ok {
			continue
		}

		for _, fileContact := range candidates {
			if !contactsMatch(dbContact, fileContact) {
				continue
			}

			matches = append(matches, matchRecord{
				MatchedAt: matchedAt,
				AID:       dbContact.ID,
				BID:       fileContact.ID,
				ACallID:   dbContact.FromID,
				BCallID:   fileContact.FromID,
			})
		}
	}

	err = bulkInsertMatches(ctx, pool, matches)
	if err != nil {
		log.Panic(err)
	}

	for _, m := range matches {
		affected.Add(m.ACallID)
		affected.Add(m.BCallID)
	}

	// Queue affected callsign IDs for awards evaluation.
	// Use a Redis set for natural dedup.
	if affected.Cardinality() > 0 && rdb != nil {
		members := make([]any, 0, affected.Cardinality())
		for _, id := range affected.ToSlice() {
			members = append(members, id)
		}
		if err := rdb.SAdd(ctx, "awards_eval_pending", members...).Err(); err != nil {
			log.Printf("Warning: failed to queue awards evaluation: %v", err)
		}
	}

	duration := time.Since(start)
	fmt.Printf(
		"%-16s Processed %d contacts in %dms at %fms/contact, found %d matches\n",
		filepath.Base(filename),
		len(logfile.Records),
		duration.Milliseconds(),
		float32(duration.Milliseconds())/float32(max(len(logfile.Records), 1)),
		len(matches),
	)
}

func processUpload(
	ctx context.Context,
	pool *pgxpool.Pool,
	rdb *redis.Client,
	uploadID int64,
	uploadDir string,
) {
	var logFile, callsign string
	var replacesID *int64
	err := pool.QueryRow(ctx,
		`SELECT log_file, callsign, replaces_id FROM app_logupload WHERE id = $1`,
		uploadID,
	).Scan(&logFile, &callsign, &replacesID)
	if err != nil {
		log.Printf("Error fetching upload %d: %v", uploadID, err)
		return
	}

	// A re-upload corrects a specific earlier upload; resolve that upload's
	// file so process can diff the two.
	replacesPath := ""
	if replacesID != nil {
		var oldFile string
		err := pool.QueryRow(ctx,
			`SELECT log_file FROM app_logupload WHERE id = $1`,
			*replacesID,
		).Scan(&oldFile)
		if err != nil {
			log.Printf(
				"Error fetching replaced upload %d for upload %d: %v",
				*replacesID,
				uploadID,
				err,
			)
			return
		}
		replacesPath = filepath.Join(uploadDir, oldFile)
	}

	fullPath := filepath.Join(uploadDir, logFile)
	log.Printf("Processing upload %d: %s for %s", uploadID, fullPath, callsign)

	process(ctx, pool, rdb, fullPath, callsign, replacesPath)

	_, err = pool.Exec(ctx,
		`UPDATE app_logupload SET processed = true WHERE id = $1`,
		uploadID,
	)
	if err != nil {
		log.Printf("Error marking upload %d as processed: %v", uploadID, err)
	}
}

// sweepUnprocessed recovers uploads whose queue entry was consumed but never
// finished (a crash or panic mid-processing): processing is at-most-once from
// Redis's point of view, so the `processed` flag in app_logupload is the
// durable ledger. The age guard skips uploads that are legitimately still
// waiting in the queue; racing one anyway is harmless because processing is
// idempotent.
func sweepUnprocessed(
	ctx context.Context,
	pool *pgxpool.Pool,
	rdb *redis.Client,
	uploadDir string,
) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM app_logupload
		 WHERE processed = false AND upload_time < now() - interval '1 minute'
		 ORDER BY id`)
	if err != nil {
		log.Printf("Sweep query failed: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("Sweep scan failed: %v", err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()

	if len(ids) == 0 {
		return
	}
	log.Printf("Recovering %d unprocessed upload(s)", len(ids))
	for _, id := range ids {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Panic recovering upload %d: %v", id, r)
					sentry.CurrentHub().Recover(r)
					sentry.Flush(2 * time.Second)
				}
			}()
			processUpload(ctx, pool, rdb, id, uploadDir)
		}()
	}
}

func consumerLoop(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client, uploadDir string) {
	log.Println("Starting Redis consumer loop on log_upload_queue")
	for {
		result, err := rdb.BRPop(ctx, 5*time.Second, "log_upload_queue").Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("Redis BRPOP error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		uploadID, err := strconv.ParseInt(result[1], 10, 64)
		if err != nil {
			log.Printf("Invalid upload ID %q: %v", result[1], err)
			continue
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Panic processing upload %d: %v", uploadID, r)
					sentry.CurrentHub().Recover(r)
					sentry.Flush(2 * time.Second)
				}
			}()
			processUpload(ctx, pool, rdb, uploadID, uploadDir)
		}()
	}
}

var flagFilename = flag.String("filename", "", "the filename to import (dev mode)")
var flagCallsign = flag.String("callsign", "", "the callsign for the file (dev mode)")
var profileCpu = flag.String("profile-cpu", "", "write cpu profile to `file`")

func main() {
	ctx := context.Background()

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

	initModeMaps()

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

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pool, err := openPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	err = initDatabase(ctx, pool)
	if err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}

	// Dev mode: process a single file and exit (no Redis, no awards queue).
	// GC is disabled here for throughput: the process is short-lived, so
	// letting memory grow until exit is faster than collecting. The consumer
	// below runs indefinitely and MUST keep GC enabled.
	if *flagFilename != "" && *flagCallsign != "" {
		debug.SetGCPercent(-1)
		process(ctx, pool, nil, *flagFilename, *flagCallsign, "")
		return
	}

	// Production mode: Redis consumer
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Fatal("REDIS_URL is required (or use -filename and -callsign for dev mode)")
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	uploadDir := os.Getenv("UPLOAD_DIR")
	if uploadDir == "" {
		uploadDir = "."
	}

	sweepUnprocessed(ctx, pool, rdb, uploadDir)
	consumerLoop(ctx, pool, rdb, uploadDir)
}
