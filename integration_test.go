package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/flwyd/adif-multitool/adif"
	"github.com/flwyd/adif-multitool/adif/spec"
	_ "github.com/mattn/go-sqlite3"
)

const testADIFile = "adif-spec/316/tests/ADIF_316_test_QSOs_2025_09_15.adi"

func TestSpecTestFile(t *testing.T) {
	if _, err := os.Stat(testADIFile); os.IsNotExist(err) {
		t.Skipf("test file not found: %s", testADIFile)
	}

	// parse
	f, err := os.Open(testADIFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	logfile, err := adif.NewADIIO().Read(f)
	if err != nil {
		t.Fatalf("ADIF parse error: %v", err)
	}
	if len(logfile.Records) == 0 {
		t.Fatal("parsed 0 records")
	}
	t.Logf("parsed %d records", len(logfile.Records))

	// set up in-memory db
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := initDatabase(db); err != nil {
		t.Fatalf("initDatabase: %v", err)
	}

	// collect unique callsigns from CALL fields + a synthetic "from" callsign
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

	// insert callsigns
	callsJSON, _ := json.Marshal(callsInFile)
	callsInDB, err := queryCallsignsByJSON(db, callsJSON)
	if err != nil {
		t.Fatalf("queryCallsignsByJSON: %v", err)
	}
	callsInDB, err = bulkInsertCallsigns(db, callsInFile, callsInDB)
	if err != nil {
		t.Fatalf("bulkInsertCallsigns: %v", err)
	}

	fromId := callsInDB[fromCall]
	if fromId == 0 {
		t.Fatal("fromId is 0")
	}

	// convert records to contacts
	var contacts []Contact
	var dateStart = MaxInt
	var dateEnd = MinInt
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

	// bulk insert
	metaId, err := insertAdifMetadata(db, 1000, "test-spec-hash", "3.1.6", "test", "1.0")
	if err != nil {
		t.Fatalf("insertAdifMetadata: %v", err)
	}

	err = bulkInsertContacts(db, contacts, metaId)
	if err != nil {
		t.Fatalf("bulkInsertContacts: %v", err)
	}

	// verify rows in db match
	var count int
	db.QueryRow("SELECT count(*) FROM contacts").Scan(&count)
	if count != len(contacts) {
		t.Errorf("expected %d rows in contacts, got %d", len(contacts), count)
	}

	// verify all contacts got IDs
	for i, c := range contacts {
		if c.ID <= 0 {
			t.Errorf("contacts[%d].ID = %d, expected > 0", i, c.ID)
			break
		}
	}

	// run match query (all contacts are from N0TEST so no matches expected,
	// but this exercises findPotentialMatches against a populated db)
	potentialMatches, err := findPotentialMatches(db, fromId, dateStart-1, dateEnd+1)
	if err != nil {
		t.Fatalf("findPotentialMatches: %v", err)
	}
	t.Logf(
		"inserted %d contacts, found %d potential matches for reverse lookup",
		count,
		len(potentialMatches),
	)
}
