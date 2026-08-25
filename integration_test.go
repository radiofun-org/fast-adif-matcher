package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testADIFile = "adif-spec/317/tests/ADIF_317_test_QSOs_2026_03_22.adi"
const testADXFile = "adif-spec/317/tests/ADIF_317_test_QSOs_2026_03_22.adx"

func parseAndInsert(t *testing.T, filename string, reader adif.Reader) int {
	t.Helper()

	ctx, pool := setupTestDB(t)

	return parseAndInsertWithDB(t, ctx, pool, filename, reader)
}

func parseAndInsertWithDB(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	filename string,
	reader adif.Reader,
) int {
	t.Helper()

	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	logfile, err := reader.Read(f)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(logfile.Records) == 0 {
		t.Fatal("parsed 0 records")
	}
	t.Logf("parsed %d records from %s", len(logfile.Records), filepath.Base(filename))

	const fromCall = "N0TEST"
	calls := mapset.NewSet[string]()
	calls.Add(fromCall)
	for _, record := range logfile.Records {
		if call, ok := record.Get(spec.CallField.Name); ok {
			calls.Add(call.Value)
		}
	}
	callsInFile := calls.ToSlice()
	t.Logf("unique callsigns: %d", len(callsInFile))

	callsInDB, err := queryCallsigns(ctx, pool, callsInFile)
	if err != nil {
		t.Fatalf("queryCallsigns: %v", err)
	}
	callsInDB, err = bulkInsertCallsigns(ctx, pool, callsInFile, callsInDB)
	if err != nil {
		t.Fatalf("bulkInsertCallsigns: %v", err)
	}

	fromId := callsInDB[fromCall]
	if fromId == 0 {
		t.Fatal("fromId is 0")
	}

	var contacts []Contact
	var dateStart = math.MaxInt
	var dateEnd = math.MinInt
	errorCounts := map[string]int{}

	for _, record := range logfile.Records {
		contact, err := recordToContact(fromId, record, callsInDB)
		if err != nil {
			msg := err.Error()
			if len(msg) > 50 {
				msg = msg[:50]
			}
			errorCounts[msg]++
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
	t.Logf(
		"converted %d/%d contacts, %d with errors",
		len(contacts),
		len(logfile.Records),
		len(logfile.Records)-len(contacts),
	)
	for msg, count := range errorCounts {
		t.Logf("  %4d  %s", count, msg)
	}

	if len(contacts) == 0 {
		t.Fatal("converted 0 contacts")
	}

	metaId, err := insertAdifMetadata(ctx, pool, 1000, "test-hash", "3.1.7", "test", "1.0")
	if err != nil {
		t.Fatalf("insertAdifMetadata: %v", err)
	}

	err = bulkInsertContacts(ctx, pool, contacts, metaId)
	if err != nil {
		t.Fatalf("bulkInsertContacts: %v", err)
	}

	var count int
	pool.QueryRow(ctx, "SELECT count(*) FROM matcher_contact").Scan(&count)
	if count != len(contacts) {
		t.Errorf("expected %d rows in matcher_contact, got %d", len(contacts), count)
	}

	for i, c := range contacts {
		if c.ID <= 0 {
			t.Errorf("contacts[%d].ID = %d, expected > 0", i, c.ID)
			break
		}
	}

	potentialMatches, err := findPotentialMatches(ctx, pool, fromId, dateStart-1, dateEnd+1)
	if err != nil {
		t.Fatalf("findPotentialMatches: %v", err)
	}
	t.Logf(
		"inserted %d contacts, found %d potential matches for reverse lookup",
		count,
		len(potentialMatches),
	)

	return len(contacts)
}

func TestSpecTestFileADI(t *testing.T) {
	if _, err := os.Stat(testADIFile); os.IsNotExist(err) {
		t.Skipf("test file not found: %s", testADIFile)
	}
	parseAndInsert(t, testADIFile, adif.NewADIIO())
}

func TestSpecTestFileADX(t *testing.T) {
	if _, err := os.Stat(testADXFile); os.IsNotExist(err) {
		t.Skipf("test file not found: %s", testADXFile)
	}
	parseAndInsert(t, testADXFile, adif.NewADXIO())
}

func TestADIAndADXProduceSameContacts(t *testing.T) {
	if _, err := os.Stat(testADIFile); os.IsNotExist(err) {
		t.Skipf("test file not found: %s", testADIFile)
	}
	if _, err := os.Stat(testADXFile); os.IsNotExist(err) {
		t.Skipf("test file not found: %s", testADXFile)
	}

	adiCount := parseAndInsert(t, testADIFile, adif.NewADIIO())
	adxCount := parseAndInsert(t, testADXFile, adif.NewADXIO())

	if adiCount != adxCount {
		t.Errorf("ADI produced %d contacts but ADX produced %d", adiCount, adxCount)
	}
	t.Logf("both formats produced %d contacts", adiCount)
}

func TestReaderForExtension(t *testing.T) {
	tests := []struct {
		filename string
		wantType string
	}{
		{"log.adi", "adi"},
		{"log.adif", "adi"},
		{"LOG.ADI", "adi"},
		{"log.ADIF", "adi"},
		{"log.adx", "adx"},
		{"LOG.ADX", "adx"},
		{"path/to/file.adx", "adx"},
		{"path/to/file.adi", "adi"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			var reader adif.Reader
			if strings.EqualFold(filepath.Ext(tt.filename), ".adx") {
				reader = adif.NewADXIO()
			} else {
				reader = adif.NewADIIO()
			}

			got := reader.(interface{ String() string }).String()
			if got != tt.wantType {
				t.Errorf("for %q: got reader type %q, want %q", tt.filename, got, tt.wantType)
			}
		})
	}
}

// writeADIF writes a minimal ADIF file into dir and returns its path.
func writeADIF(t *testing.T, dir, name string, records ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "generated for test\n<EOH>\n" + strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func countRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

func TestProcessReplacesCorrectedCallsign(t *testing.T) {
	initModeMaps()
	ctx, pool := setupTestDB(t)
	dir := t.TempDir()

	// K3LR logs a QSO with W1AW.
	bFile := writeADIF(t, dir, "k3lr.adi",
		"<CALL:4>W1AW<QSO_DATE:8>20240601<TIME_ON:4>0600<BAND:3>20m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, bFile, "K3LR", "")

	// W1AW logs the same QSO but with the wrong callsign, so nothing matches.
	wrongFile := writeADIF(t, dir, "w1aw-wrong.adi",
		"<CALL:4>K3LX<QSO_DATE:8>20240601<TIME_ON:4>0601<BAND:3>20m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, wrongFile, "W1AW", "")

	if n := countRow(t, ctx, pool, "SELECT count(*) FROM matcher_match"); n != 0 {
		t.Fatalf("expected no matches with the wrong callsign, got %d", n)
	}

	// Corrected re-upload anchored to the wrong file: the K3LX contact is
	// stale (present in old, absent in new) and must be deleted; the
	// corrected contact matches K3LR's log.
	fixedFile := writeADIF(t, dir, "w1aw-fixed.adi",
		"<CALL:4>K3LR<QSO_DATE:8>20240601<TIME_ON:4>0601<BAND:3>20m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, fixedFile, "W1AW", wrongFile)

	if n := countRow(t, ctx, pool, "SELECT count(*) FROM matcher_match"); n != 1 {
		t.Fatalf("expected 1 match after correction, got %d", n)
	}
	if n := countRow(t, ctx, pool, `SELECT count(*) FROM matcher_contact c
		JOIN matcher_callsign k ON k.id = c.to_id WHERE k.callsign = 'K3LX'`); n != 0 {
		t.Fatalf("expected the wrong-callsign contact to be deleted, got %d", n)
	}
	if n := countRow(t, ctx, pool, `SELECT count(*) FROM matcher_contact c
		JOIN matcher_callsign k ON k.id = c.from_id WHERE k.callsign = 'W1AW'`); n != 1 {
		t.Fatalf("expected exactly 1 contact from W1AW, got %d", n)
	}
}

func TestProcessRevalidatesChangedBand(t *testing.T) {
	initModeMaps()
	ctx, pool := setupTestDB(t)
	dir := t.TempDir()

	bFile := writeADIF(t, dir, "k3lr.adi",
		"<CALL:4>W1AW<QSO_DATE:8>20240601<TIME_ON:4>0600<BAND:3>20m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, bFile, "K3LR", "")

	aFile := writeADIF(t, dir, "w1aw.adi",
		"<CALL:4>K3LR<QSO_DATE:8>20240601<TIME_ON:4>0601<BAND:3>20m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, aFile, "W1AW", "")

	if n := countRow(t, ctx, pool, "SELECT count(*) FROM matcher_match"); n != 1 {
		t.Fatalf("expected 1 match, got %d", n)
	}

	// Same identity key (call, date, time) but the band was corrected: the
	// upsert updates the row in place and the existing match no longer
	// satisfies the rules, so revalidation must delete it. A plain re-upload
	// suffices; no replaces anchor is needed for field corrections.
	aFixed := writeADIF(t, dir, "w1aw-fixed.adi",
		"<CALL:4>K3LR<QSO_DATE:8>20240601<TIME_ON:4>0601<BAND:3>40m<MODE:3>FT8<EOR>")
	process(ctx, pool, nil, aFixed, "W1AW", "")

	if n := countRow(t, ctx, pool, "SELECT count(*) FROM matcher_match"); n != 0 {
		t.Fatalf("expected the stale match to be deleted after band correction, got %d", n)
	}
}
