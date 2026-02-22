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

func withinThirtyMinutes(a Contact, b Contact) bool {
	if abs(a.Date-b.Date) > 1 {
		return false
	}
	if a.Date < b.Date {
		return (1440-a.Time)+b.Time <= 30
	}
	if a.Date > b.Date {
		return (1440-b.Time)+a.Time <= 30
	}
	return abs(a.Time-b.Time) <= 30
}

func process(ctx context.Context, pool *pgxpool.Pool, filename string, callsign string) {
	debug.SetGCPercent(-1)
	start := time.Now()

	var adifIo adif.Reader
	if strings.EqualFold(filepath.Ext(filename), ".adx") {
		adifIo = adif.NewADXIO()
	} else {
		adifIo = adif.NewADIIO()
	}

	f, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

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

	calls := mapset.NewSet[string]()
	calls.Add(callsign)

	for _, record := range logfile.Records {
		call, ok := record.Get(spec.CallField.Name)
		if ok {
			calls.Add(call.Value)
		}
	}

	callsInFile := calls.ToSlice()

	callsInDatabase, err := queryCallsigns(ctx, pool, callsInFile)
	if err != nil {
		log.Panic(err)
	}

	callsInDatabase, err = bulkInsertCallsigns(ctx, pool, callsInFile, callsInDatabase)
	if err != nil {
		log.Panic(err)
	}

	fromId := callsInDatabase[callsign]

	var dateStart = math.MaxInt
	var dateEnd = math.MinInt

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

	err = bulkInsertContacts(ctx, pool, contacts, adifMetadataId)
	if err != nil {
		log.Panic(err)
	}

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

	err = bulkInsertMatches(ctx, pool, matches)
	if err != nil {
		log.Panic(err)
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

func processUpload(ctx context.Context, pool *pgxpool.Pool, uploadID int64, uploadDir string) {
	var logFile, callsign string
	err := pool.QueryRow(ctx,
		`SELECT log_file, callsign FROM app_logupload WHERE id = $1`,
		uploadID,
	).Scan(&logFile, &callsign)
	if err != nil {
		log.Printf("Error fetching upload %d: %v", uploadID, err)
		return
	}

	fullPath := filepath.Join(uploadDir, logFile)
	log.Printf("Processing upload %d: %s for %s", uploadID, fullPath, callsign)

	process(ctx, pool, fullPath, callsign)

	_, err = pool.Exec(ctx,
		`UPDATE app_logupload SET processed = true WHERE id = $1`,
		uploadID,
	)
	if err != nil {
		log.Printf("Error marking upload %d as processed: %v", uploadID, err)
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
			processUpload(ctx, pool, uploadID, uploadDir)
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

	// Dev mode: process a single file and exit
	if *flagFilename != "" && *flagCallsign != "" {
		process(ctx, pool, *flagFilename, *flagCallsign)
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

	consumerLoop(ctx, pool, rdb, uploadDir)
}
