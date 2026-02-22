package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		pgContainer.Terminate(ctx)
		log.Fatalf("failed to get connection string: %v", err)
	}

	testPool, err = openPool(ctx, connStr)
	if err != nil {
		pgContainer.Terminate(ctx)
		log.Fatalf("failed to open pool: %v", err)
	}

	if err := initDatabase(ctx, testPool); err != nil {
		testPool.Close()
		pgContainer.Terminate(ctx)
		log.Fatalf("initDatabase: %v", err)
	}

	code := m.Run()

	testPool.Close()
	pgContainer.Terminate(ctx)
	os.Exit(code)
}

func setupTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Truncate all tables to give each test a clean slate
	for _, table := range []string{"matches", "contacts", "adif_metadata", "callsigns"} {
		if _, err := testPool.Exec(ctx, fmt.Sprintf("TRUNCATE %s CASCADE", table)); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}

	return ctx, testPool
}

func TestInitDatabase(t *testing.T) {
	ctx, pool := setupTestDB(t)

	// calling initDatabase again should be idempotent
	if err := initDatabase(ctx, pool); err != nil {
		t.Fatalf("second initDatabase failed: %v", err)
	}
}

func TestAddCallsign(t *testing.T) {
	ctx, pool := setupTestDB(t)

	id1, err := addCallsign(ctx, pool, "W1AW")
	if err != nil {
		t.Fatalf("addCallsign: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("expected id > 0, got %d", id1)
	}

	id2, err := addCallsign(ctx, pool, "W1AW")
	if err != nil {
		t.Fatalf("addCallsign duplicate: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("expected same id %d, got %d", id1, id2)
	}

	id3, err := addCallsign(ctx, pool, "K3LR")
	if err != nil {
		t.Fatalf("addCallsign new: %v", err)
	}
	if id3 == id1 {
		t.Fatalf("expected different id from %d", id1)
	}
}

func TestInsertAdifMetadata(t *testing.T) {
	ctx, pool := setupTestDB(t)

	id, err := insertAdifMetadata(ctx, pool, 1000, "abc123", "3.1.4", "test", "1.0")
	if err != nil {
		t.Fatalf("insertAdifMetadata: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected id > 0, got %d", id)
	}

	// duplicate hash returns 0
	id2, err := insertAdifMetadata(ctx, pool, 2000, "abc123", "3.1.4", "test", "1.0")
	if err != nil {
		t.Fatalf("insertAdifMetadata duplicate: %v", err)
	}
	if id2 != 0 {
		t.Fatalf("expected 0 for duplicate hash, got %d", id2)
	}

	// different hash returns new id
	id3, err := insertAdifMetadata(ctx, pool, 3000, "def456", "3.1.4", "test", "1.0")
	if err != nil {
		t.Fatalf("insertAdifMetadata new hash: %v", err)
	}
	if id3 <= 0 || id3 == id {
		t.Fatalf("expected new id > 0 and != %d, got %d", id, id3)
	}
}

func TestQueryCallsigns(t *testing.T) {
	ctx, pool := setupTestDB(t)

	addCallsign(ctx, pool, "W1AW")
	addCallsign(ctx, pool, "K3LR")

	calls := []string{"W1AW", "K3LR", "N0CALL"}

	result, err := queryCallsigns(ctx, pool, calls)
	if err != nil {
		t.Fatalf("queryCallsigns: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if _, ok := result["W1AW"]; !ok {
		t.Fatal("missing W1AW")
	}
	if _, ok := result["K3LR"]; !ok {
		t.Fatal("missing K3LR")
	}
	if _, ok := result["N0CALL"]; ok {
		t.Fatal("N0CALL should not be present")
	}
}

func TestBulkInsertCallsigns(t *testing.T) {
	ctx, pool := setupTestDB(t)

	id1, _ := addCallsign(ctx, pool, "W1AW")
	existing := map[string]int64{"W1AW": id1}

	calls := []string{"W1AW", "K3LR", "VE3XYZ"}
	result, err := bulkInsertCallsigns(ctx, pool, calls, existing)
	if err != nil {
		t.Fatalf("bulkInsertCallsigns: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 callsigns, got %d", len(result))
	}
	if result["W1AW"] != id1 {
		t.Fatalf("W1AW id changed from %d to %d", id1, result["W1AW"])
	}
	if result["K3LR"] <= 0 {
		t.Fatal("K3LR should have id > 0")
	}
	if result["VE3XYZ"] <= 0 {
		t.Fatal("VE3XYZ should have id > 0")
	}
}

func TestBulkInsertContacts(t *testing.T) {
	ctx, pool := setupTestDB(t)

	metaId, _ := insertAdifMetadata(ctx, pool, 1000, "hash1", "3.1.4", "test", "1.0")
	fromId, _ := addCallsign(ctx, pool, "W1AW")
	toId, _ := addCallsign(ctx, pool, "K3LR")

	contacts := []Contact{
		{Date: 27000, Time: 600, FromID: fromId, ToID: toId, Band: 8, Mode: 29},
		{Date: 27000, Time: 610, FromID: fromId, ToID: toId, Band: 8, Mode: 29},
	}

	err := bulkInsertContacts(ctx, pool, contacts, metaId)
	if err != nil {
		t.Fatalf("bulkInsertContacts: %v", err)
	}

	if contacts[0].ID <= 0 {
		t.Fatalf("contacts[0].ID should be > 0, got %d", contacts[0].ID)
	}
	if contacts[1].ID <= 0 {
		t.Fatalf("contacts[1].ID should be > 0, got %d", contacts[1].ID)
	}
	if contacts[0].ID == contacts[1].ID {
		t.Fatal("contacts should have different IDs")
	}

	// upsert: same (date, time, from_id, to_id) should update and return same id
	contacts2 := []Contact{
		{Date: 27000, Time: 600, FromID: fromId, ToID: toId, Band: 6, Mode: 14},
	}
	err = bulkInsertContacts(ctx, pool, contacts2, metaId)
	if err != nil {
		t.Fatalf("bulkInsertContacts upsert: %v", err)
	}
	if contacts2[0].ID != contacts[0].ID {
		t.Fatalf("upsert should return same id %d, got %d", contacts[0].ID, contacts2[0].ID)
	}

	// verify the band was updated
	var band int
	err = pool.QueryRow(ctx, "SELECT band FROM contacts WHERE id = $1", contacts[0].ID).Scan(&band)
	if err != nil {
		t.Fatal(err)
	}
	if band != 6 {
		t.Fatalf("expected band 6 after upsert, got %d", band)
	}
}

func TestBulkInsertContactsSigCoalesce(t *testing.T) {
	ctx, pool := setupTestDB(t)

	metaId, _ := insertAdifMetadata(ctx, pool, 1000, "hash1", "3.1.4", "test", "1.0")
	fromId, _ := addCallsign(ctx, pool, "W1AW")
	toId, _ := addCallsign(ctx, pool, "K3LR")

	// Upload 1: POTA activation
	contacts1 := []Contact{
		{
			Date: 27000, Time: 600, FromID: fromId, ToID: toId,
			Band: 8, Mode: 29,
			MyPOTA: "K-0059",
			MySig:  "POTA", MySigInfo: "K-0059",
		},
	}
	err := bulkInsertContacts(ctx, pool, contacts1, metaId)
	if err != nil {
		t.Fatalf("insert upload 1: %v", err)
	}
	id := contacts1[0].ID

	// Verify POTA and sig fields are stored
	var pota, iota_, sig, sigInfo *string
	err = pool.QueryRow(ctx, "SELECT my_pota, my_iota, my_sig, my_sig_info FROM contacts WHERE id = $1", id).
		Scan(&pota, &iota_, &sig, &sigInfo)
	if err != nil {
		t.Fatal(err)
	}
	if pota == nil || *pota != "K-0059" {
		t.Fatalf("expected my_pota=K-0059, got %v", pota)
	}
	if iota_ != nil {
		t.Fatalf("expected my_iota=NULL, got %v", *iota_)
	}
	if sig == nil || *sig != "POTA" {
		t.Fatalf("expected my_sig=POTA, got %v", sig)
	}
	if sigInfo == nil || *sigInfo != "K-0059" {
		t.Fatalf("expected my_sig_info=K-0059, got %v", sigInfo)
	}

	// Upload 2: same QSO but now with IOTA (no POTA this time)
	contacts2 := []Contact{
		{
			Date: 27000, Time: 600, FromID: fromId, ToID: toId,
			Band: 8, Mode: 29,
			MyIOTA: "NA-148",
			MySig:  "IOTA", MySigInfo: "NA-148",
		},
	}
	err = bulkInsertContacts(ctx, pool, contacts2, metaId)
	if err != nil {
		t.Fatalf("insert upload 2: %v", err)
	}

	if contacts2[0].ID != id {
		t.Fatalf("expected same id %d, got %d", id, contacts2[0].ID)
	}

	// Both POTA and IOTA should be present; sig fields updated to IOTA
	err = pool.QueryRow(ctx, "SELECT my_pota, my_iota, my_sig, my_sig_info FROM contacts WHERE id = $1", id).
		Scan(&pota, &iota_, &sig, &sigInfo)
	if err != nil {
		t.Fatal(err)
	}
	if pota == nil || *pota != "K-0059" {
		t.Fatalf("POTA should survive second upload, got %v", pota)
	}
	if iota_ == nil || *iota_ != "NA-148" {
		t.Fatalf("IOTA should be filled by second upload, got %v", iota_)
	}
	if sig == nil || *sig != "IOTA" {
		t.Fatalf("my_sig should be updated to IOTA, got %v", sig)
	}
	if sigInfo == nil || *sigInfo != "NA-148" {
		t.Fatalf("my_sig_info should be updated to NA-148, got %v", sigInfo)
	}

	// Upload 3: explicit new POTA ref should overwrite the old one
	contacts3 := []Contact{
		{
			Date: 27000, Time: 600, FromID: fromId, ToID: toId,
			Band: 8, Mode: 29,
			MyPOTA: "K-1234",
		},
	}
	err = bulkInsertContacts(ctx, pool, contacts3, metaId)
	if err != nil {
		t.Fatalf("insert upload 3: %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT my_pota, my_iota FROM contacts WHERE id = $1", id).
		Scan(&pota, &iota_)
	if err != nil {
		t.Fatal(err)
	}
	if pota == nil || *pota != "K-1234" {
		t.Fatalf("POTA should be updated to K-1234, got %v", pota)
	}
	if iota_ == nil || *iota_ != "NA-148" {
		t.Fatalf("IOTA should still be NA-148, got %v", iota_)
	}
}

func TestBulkInsertContactsCustomSig(t *testing.T) {
	ctx, pool := setupTestDB(t)

	metaId, _ := insertAdifMetadata(ctx, pool, 1000, "hash1", "3.1.4", "test", "1.0")
	fromId, _ := addCallsign(ctx, pool, "W1AW")
	toId, _ := addCallsign(ctx, pool, "K3LR")

	// Upload with a custom sig that isn't POTA/SOTA/WWFF/IOTA
	contacts := []Contact{
		{
			Date: 27000, Time: 600, FromID: fromId, ToID: toId,
			Band: 8, Mode: 29,
			MySig: "BOTA", MySigInfo: "ENG-0042",
		},
	}
	err := bulkInsertContacts(ctx, pool, contacts, metaId)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id := contacts[0].ID

	var sig, sigInfo, pota, sota, wwff, iota_ *string
	err = pool.QueryRow(
		ctx,
		"SELECT my_sig, my_sig_info, my_pota, my_sota, my_wwff, my_iota FROM contacts WHERE id = $1",
		id,
	).Scan(&sig, &sigInfo, &pota, &sota, &wwff, &iota_)
	if err != nil {
		t.Fatal(err)
	}

	if sig == nil || *sig != "BOTA" {
		t.Fatalf("expected my_sig=BOTA, got %v", sig)
	}
	if sigInfo == nil || *sigInfo != "ENG-0042" {
		t.Fatalf("expected my_sig_info=ENG-0042, got %v", sigInfo)
	}

	for name, val := range map[string]*string{
		"my_pota": pota, "my_sota": sota, "my_wwff": wwff, "my_iota": iota_,
	} {
		if val != nil {
			t.Fatalf("expected %s=NULL for custom sig, got %v", name, *val)
		}
	}

	// Second upload adds POTA alongside the custom sig
	contacts2 := []Contact{
		{
			Date: 27000, Time: 600, FromID: fromId, ToID: toId,
			Band: 8, Mode: 29,
			MyPOTA: "K-0059",
		},
	}
	err = bulkInsertContacts(ctx, pool, contacts2, metaId)
	if err != nil {
		t.Fatalf("insert upload 2: %v", err)
	}

	err = pool.QueryRow(ctx,
		"SELECT my_sig, my_sig_info, my_pota FROM contacts WHERE id = $1", id,
	).Scan(&sig, &sigInfo, &pota)
	if err != nil {
		t.Fatal(err)
	}

	if sig == nil || *sig != "BOTA" {
		t.Fatalf("custom sig should survive, got %v", sig)
	}
	if sigInfo == nil || *sigInfo != "ENG-0042" {
		t.Fatalf("custom sig_info should survive, got %v", sigInfo)
	}
	if pota == nil || *pota != "K-0059" {
		t.Fatalf("POTA should be filled, got %v", pota)
	}
}

func TestFindPotentialMatches(t *testing.T) {
	ctx, pool := setupTestDB(t)

	metaId, _ := insertAdifMetadata(ctx, pool, 1000, "hash1", "3.1.4", "test", "1.0")
	call1, _ := addCallsign(ctx, pool, "W1AW")
	call2, _ := addCallsign(ctx, pool, "K3LR")
	call3, _ := addCallsign(ctx, pool, "VE3XYZ")

	contacts := []Contact{
		{Date: 27000, Time: 600, FromID: call1, ToID: call2, Band: 8, Mode: 29},
		{Date: 27001, Time: 700, FromID: call2, ToID: call1, Band: 8, Mode: 29},
		{Date: 27005, Time: 800, FromID: call3, ToID: call1, Band: 8, Mode: 29},
	}
	bulkInsertContacts(ctx, pool, contacts, metaId)

	results, err := findPotentialMatches(ctx, pool, call1, 27000, 27002)
	if err != nil {
		t.Fatalf("findPotentialMatches: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0].FromID != call2 {
		t.Fatalf("expected from_id %d, got %d", call2, results[0].FromID)
	}

	results2, err := findPotentialMatches(ctx, pool, call1, 27000, 27006)
	if err != nil {
		t.Fatalf("findPotentialMatches wide: %v", err)
	}
	if len(results2) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(results2))
	}
}

func TestBulkInsertMatches(t *testing.T) {
	ctx, pool := setupTestDB(t)

	metaId, _ := insertAdifMetadata(ctx, pool, 1000, "hash1", "3.1.4", "test", "1.0")
	call1, _ := addCallsign(ctx, pool, "W1AW")
	call2, _ := addCallsign(ctx, pool, "K3LR")

	contacts := []Contact{
		{Date: 27000, Time: 600, FromID: call1, ToID: call2, Band: 8, Mode: 29},
		{Date: 27000, Time: 601, FromID: call2, ToID: call1, Band: 8, Mode: 29},
	}
	bulkInsertContacts(ctx, pool, contacts, metaId)

	matches := []matchRecord{
		{
			MatchedAt: 9999,
			AID:       contacts[0].ID,
			BID:       contacts[1].ID,
			ACallID:   call1,
			BCallID:   call2,
		},
	}

	err := bulkInsertMatches(ctx, pool, matches)
	if err != nil {
		t.Fatalf("bulkInsertMatches: %v", err)
	}

	var count int
	pool.QueryRow(ctx, "SELECT count(*) FROM matches").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 match row, got %d", count)
	}

	// duplicate insert should be ignored (ON CONFLICT DO NOTHING)
	err = bulkInsertMatches(ctx, pool, matches)
	if err != nil {
		t.Fatalf("bulkInsertMatches duplicate: %v", err)
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM matches").Scan(&count)
	if count != 1 {
		t.Fatalf("expected still 1 match row after duplicate, got %d", count)
	}
}
