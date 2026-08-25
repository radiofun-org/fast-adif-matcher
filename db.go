package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type matchRecord struct {
	MatchedAt, AID, BID, ACallID, BCallID int64
}

// contactKey is a contact's identity within one station's log: who was
// worked, and when. from_id is implicit (the uploading station).
type contactKey struct {
	ToID       int64
	Date, Time int
}

func openPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	return pgxpool.NewWithConfig(ctx, cfg)
}

// initDatabase creates the matcher tables if they don't exist. The website's
// Django migrations (contacts/migrations/) are the authoritative schema — they
// also add read-path indexes the matcher doesn't need. This DDL exists so the
// matcher runs standalone against a fresh Postgres (the perf loop, test.sh,
// testcontainers) without needing Django; keep it in sync with the Django
// models when either changes.
func initDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS matcher_contact (
			id BIGSERIAL PRIMARY KEY,
			adif_metadata_id BIGINT,
			date SMALLINT,
			time SMALLINT,
			time_off SMALLINT,
			from_id BIGINT,
			to_id BIGINT,
			band SMALLINT,
			mode SMALLINT,
			sub_mode SMALLINT,
			grid BIGINT,
			grid_size SMALLINT,
			administrative_primary TEXT,
			administrative_secondary TEXT,
			dxcc SMALLINT,
			zone_cq SMALLINT,
			zone_itu SMALLINT,
			tx_power SMALLINT,
			my_pota TEXT[],
			my_sota TEXT,
			my_wwff TEXT,
			my_iota TEXT,
			my_sig TEXT,
			my_sig_info TEXT,
			UNIQUE(to_id, date, time, from_id)
		)`)
	if err != nil {
		return err
	}

	// Migrate my_pota from TEXT to TEXT[] for existing databases
	_, err = pool.Exec(ctx, `
		DO $$ BEGIN
			ALTER TABLE matcher_contact ALTER COLUMN my_pota TYPE TEXT[]
				USING CASE WHEN my_pota IS NOT NULL THEN string_to_array(my_pota, ',') ELSE NULL END;
		EXCEPTION WHEN others THEN NULL;
		END $$`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS matcher_adif_metadata (
			id BIGSERIAL PRIMARY KEY,
			process_time BIGINT,
			file_hash TEXT UNIQUE,
			adif_version TEXT,
			program_id TEXT,
			program_version TEXT
		)`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS matcher_match (
			id BIGSERIAL PRIMARY KEY,
			matched_at BIGINT,
			a_id BIGINT,
			b_id BIGINT,
			a_call_id BIGINT,
			b_call_id BIGINT,
			UNIQUE (a_id, b_id)
		)`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS matcher_callsign (
			id BIGSERIAL PRIMARY KEY,
			callsign TEXT UNIQUE
		)`)
	if err != nil {
		return err
	}

	return nil
}

func addCallsign(ctx context.Context, pool *pgxpool.Pool, callsign string) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO matcher_callsign (callsign) VALUES ($1)
		 ON CONFLICT(callsign) DO UPDATE SET callsign=matcher_callsign.callsign
		 RETURNING id`,
		callsign,
	).Scan(&id)
	return id, err
}

func insertAdifMetadata(
	ctx context.Context,
	pool *pgxpool.Pool,
	processTime int64,
	fileHash, adifVersion, programId, programVersion string,
) (int64, error) {
	var id int64
	err := pool.QueryRow(
		ctx,
		`INSERT INTO matcher_adif_metadata (process_time, file_hash, adif_version, program_id, program_version)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (file_hash) DO UPDATE SET process_time = excluded.process_time
		 RETURNING id`,
		processTime,
		fileHash,
		adifVersion,
		programId,
		programVersion,
	).Scan(&id)
	return id, err
}

func queryCallsigns(
	ctx context.Context,
	pool *pgxpool.Pool,
	callsigns []string,
) (map[string]int64, error) {
	result := map[string]int64{}
	rows, err := pool.Query(ctx,
		`SELECT id, callsign FROM matcher_callsign WHERE callsign = ANY($1)`,
		callsigns,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var callsign string
		if err := rows.Scan(&id, &callsign); err != nil {
			return result, err
		}
		result[callsign] = id
	}
	return result, rows.Err()
}

// bulkInsertCallsigns inserts any callsigns not already in `existing` using
// the COPY + temp table pattern for speed. Returns the updated map with all
// callsign→ID mappings.
func bulkInsertCallsigns(
	ctx context.Context,
	pool *pgxpool.Pool,
	calls []string,
	existing map[string]int64,
) (map[string]int64, error) {
	var newCalls []string
	for _, call := range calls {
		if _, ok := existing[call]; !ok {
			newCalls = append(newCalls, call)
		}
	}
	if len(newCalls) == 0 {
		return existing, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return existing, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `CREATE TEMP TABLE _tmp_callsigns (callsign TEXT) ON COMMIT DROP`)
	if err != nil {
		return existing, err
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"_tmp_callsigns"},
		[]string{"callsign"},
		pgx.CopyFromSlice(len(newCalls), func(i int) ([]any, error) {
			return []any{newCalls[i]}, nil
		}),
	)
	if err != nil {
		return existing, err
	}

	rows, err := tx.Query(ctx,
		`INSERT INTO matcher_callsign (callsign)
		 SELECT callsign FROM _tmp_callsigns
		 ON CONFLICT(callsign) DO UPDATE SET callsign=matcher_callsign.callsign
		 RETURNING id, callsign`,
	)
	if err != nil {
		return existing, err
	}
	for rows.Next() {
		var id int64
		var cs string
		if err := rows.Scan(&id, &cs); err != nil {
			rows.Close()
			return existing, err
		}
		existing[cs] = id
	}
	if err := rows.Err(); err != nil {
		return existing, err
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return existing, err
	}
	return existing, nil
}

// bulkInsertContacts upserts contacts using COPY into a temp table followed
// by INSERT ... ON CONFLICT. The upsert uses COALESCE for special activity
// fields (POTA, SOTA, WWFF, IOTA, SIG) so that re-uploading a log with
// different activity references merges them rather than overwriting with NULLs.
// The _ord column preserves insertion order so we can map returned IDs back
// to the original slice.
func bulkInsertContacts(
	ctx context.Context,
	pool *pgxpool.Pool,
	contacts []Contact,
	adifMetadataId int64,
) error {
	if len(contacts) == 0 {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `CREATE TEMP TABLE _tmp_contacts (
		_ord INTEGER,
		adif_metadata_id BIGINT,
		date SMALLINT, time SMALLINT, time_off SMALLINT,
		from_id BIGINT, to_id BIGINT,
		band SMALLINT, mode SMALLINT, sub_mode SMALLINT,
		grid BIGINT, grid_size SMALLINT,
		administrative_primary TEXT, administrative_secondary TEXT,
		dxcc SMALLINT, zone_cq SMALLINT, zone_itu SMALLINT, tx_power SMALLINT,
		my_pota TEXT[], my_sota TEXT, my_wwff TEXT, my_iota TEXT,
		my_sig TEXT, my_sig_info TEXT
	) ON COMMIT DROP`)
	if err != nil {
		return err
	}

	cols := []string{
		"_ord",
		"adif_metadata_id", "date", "time", "time_off",
		"from_id", "to_id", "band", "mode", "sub_mode",
		"grid", "grid_size",
		"administrative_primary", "administrative_secondary",
		"dxcc", "zone_cq", "zone_itu", "tx_power",
		"my_pota", "my_sota", "my_wwff", "my_iota",
		"my_sig", "my_sig_info",
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"_tmp_contacts"},
		cols,
		pgx.CopyFromSlice(len(contacts), func(i int) ([]any, error) {
			c := &contacts[i]
			return []any{
				int32(i),
				adifMetadataId,
				int16(c.Date), int16(c.Time), int16(c.TimeOff),
				c.FromID, c.ToID,
				int16(c.Band), int16(c.Mode), int16(c.SubMode),
				int64(c.Grid), int16(c.GridSize),
				nilIfEmpty(c.AdministrativePrimary), nilIfEmpty(c.AdministrativeSecondary),
				int16(c.DXCC), int16(c.ZoneCQ), int16(c.ZoneITU), int16(c.TransmitPower),
				nilIfEmptySlice(c.MyPOTA), nilIfEmpty(c.MySOTA),
				nilIfEmpty(c.MyWWFF), nilIfEmpty(c.MyIOTA),
				nilIfEmpty(c.MySig), nilIfEmpty(c.MySigInfo),
			}, nil
		}),
	)
	if err != nil {
		return err
	}

	// Deduplicate: if the ADIF has duplicate QSOs (same to_id, date, time,
	// from_id), keep only the last occurrence. Postgres doesn't allow ON
	// CONFLICT DO UPDATE to hit the same row twice in one statement.
	_, err = tx.Exec(ctx, `DELETE FROM _tmp_contacts a USING _tmp_contacts b
		WHERE a.to_id = b.to_id AND a.date = b.date AND a.time = b.time AND a.from_id = b.from_id
		AND a._ord < b._ord`)
	if err != nil {
		return err
	}

	rows, err := tx.Query(ctx,
		`WITH ins AS (
			INSERT INTO matcher_contact (
				adif_metadata_id, date, time, time_off,
				from_id, to_id, band, mode, sub_mode,
				grid, grid_size,
				administrative_primary, administrative_secondary,
				dxcc, zone_cq, zone_itu, tx_power,
				my_pota, my_sota, my_wwff, my_iota,
				my_sig, my_sig_info
			)
			SELECT
				adif_metadata_id, date, time, time_off,
				from_id, to_id, band, mode, sub_mode,
				grid, grid_size,
				administrative_primary, administrative_secondary,
				dxcc, zone_cq, zone_itu, tx_power,
				my_pota, my_sota, my_wwff, my_iota,
				my_sig, my_sig_info
			FROM _tmp_contacts
			ON CONFLICT(to_id, date, time, from_id) DO UPDATE SET
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
				tx_power=excluded.tx_power,
				my_pota=COALESCE(excluded.my_pota,matcher_contact.my_pota),
				my_sota=COALESCE(excluded.my_sota,matcher_contact.my_sota),
				my_wwff=COALESCE(excluded.my_wwff,matcher_contact.my_wwff),
				my_iota=COALESCE(excluded.my_iota,matcher_contact.my_iota),
				my_sig=COALESCE(excluded.my_sig,matcher_contact.my_sig),
				my_sig_info=COALESCE(excluded.my_sig_info,matcher_contact.my_sig_info)
			RETURNING matcher_contact.id, matcher_contact.to_id, matcher_contact.date, matcher_contact.time, matcher_contact.from_id
		)
		SELECT ins.id, t._ord FROM ins
		JOIN _tmp_contacts t ON t.to_id = ins.to_id AND t.date = ins.date AND t.time = ins.time AND t.from_id = ins.from_id
		ORDER BY t._ord`)
	if err != nil {
		return err
	}

	for rows.Next() {
		var id int64
		var ord int
		if err := rows.Scan(&id, &ord); err != nil {
			rows.Close()
			return err
		}
		contacts[ord].ID = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	return tx.Commit(ctx)
}

// findPotentialMatches returns contacts already in the DB where someone else
// logged a QSO *to* the given callsign within the given date range. These are
// candidates for matching against the contacts being imported.
func findPotentialMatches(
	ctx context.Context,
	pool *pgxpool.Pool,
	fromId int64,
	dateStart int,
	dateEnd int,
) ([]Contact, error) {
	var contacts []Contact

	rows, err := pool.Query(ctx,
		`SELECT id, date, time, time_off, from_id, to_id, band, mode, grid, grid_size
		 FROM matcher_contact
		 WHERE to_id = $1 AND date >= $2 AND date <= $3`,
		fromId, dateStart, dateEnd,
	)
	if err != nil {
		return contacts, err
	}
	defer rows.Close()

	for rows.Next() {
		var c Contact
		if err := rows.Scan(
			&c.ID, &c.Date, &c.Time, &c.TimeOff,
			&c.FromID, &c.ToID, &c.Band, &c.Mode,
			&c.Grid, &c.GridSize,
		); err != nil {
			return contacts, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func bulkInsertMatches(ctx context.Context, pool *pgxpool.Pool, matches []matchRecord) error {
	if len(matches) == 0 {
		return nil
	}

	// Canonicalize so a_id < b_id. The same QSO is discovered from both
	// directions (once per side's upload); without a canonical order the
	// UNIQUE (a_id, b_id) constraint can't stop the reversed duplicate.
	// Call IDs swap together with their contact IDs to keep them paired.
	for i := range matches {
		m := &matches[i]
		if m.AID > m.BID {
			m.AID, m.BID = m.BID, m.AID
			m.ACallID, m.BCallID = m.BCallID, m.ACallID
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `CREATE TEMP TABLE _tmp_matches (
		matched_at BIGINT, a_id BIGINT, b_id BIGINT,
		a_call_id BIGINT, b_call_id BIGINT
	) ON COMMIT DROP`)
	if err != nil {
		return err
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"_tmp_matches"},
		[]string{"matched_at", "a_id", "b_id", "a_call_id", "b_call_id"},
		pgx.CopyFromSlice(len(matches), func(i int) ([]any, error) {
			m := &matches[i]
			return []any{m.MatchedAt, m.AID, m.BID, m.ACallID, m.BCallID}, nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO matcher_match (matched_at, a_id, b_id, a_call_id, b_call_id)
		 SELECT matched_at, a_id, b_id, a_call_id, b_call_id FROM _tmp_matches
		 ON CONFLICT (a_id, b_id) DO NOTHING`)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// deleteContactsByKeys deletes fromId's contacts matching the identity keys
// and returns the deleted contact IDs.
func deleteContactsByKeys(
	ctx context.Context,
	pool *pgxpool.Pool,
	fromId int64,
	keys []contactKey,
) ([]int64, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	toIDs := make([]int64, len(keys))
	dates := make([]int16, len(keys))
	times := make([]int16, len(keys))
	for i, k := range keys {
		toIDs[i] = k.ToID
		dates[i] = int16(k.Date)
		times[i] = int16(k.Time)
	}

	rows, err := pool.Query(ctx,
		`DELETE FROM matcher_contact c
		 USING unnest($2::bigint[], $3::smallint[], $4::smallint[]) AS s(to_id, date, "time")
		 WHERE c.from_id = $1 AND c.to_id = s.to_id AND c.date = s.date AND c.time = s."time"
		 RETURNING c.id`,
		fromId, toIDs, dates, times,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deleted []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		deleted = append(deleted, id)
	}
	return deleted, rows.Err()
}

// deleteMatchesReferencing deletes matches that point at any of the given
// contact IDs and returns the callsign IDs on either side of the deleted
// matches, whose award progress needs re-evaluation.
func deleteMatchesReferencing(
	ctx context.Context,
	pool *pgxpool.Pool,
	contactIDs []int64,
) ([]int64, error) {
	if len(contactIDs) == 0 {
		return nil, nil
	}

	rows, err := pool.Query(ctx,
		`DELETE FROM matcher_match
		 WHERE a_id = ANY($1) OR b_id = ANY($1)
		 RETURNING a_call_id, b_call_id`,
		contactIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	affected := map[int64]bool{}
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		affected[a] = true
		affected[b] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	return ids, nil
}

// fetchContactsByID returns the fields needed to re-run the match rules.
func fetchContactsByID(
	ctx context.Context,
	pool *pgxpool.Pool,
	ids []int64,
) (map[int64]Contact, error) {
	result := map[int64]Contact{}
	rows, err := pool.Query(ctx,
		`SELECT id, date, time, time_off, from_id, to_id, band, mode
		 FROM matcher_contact WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var c Contact
		if err := rows.Scan(
			&c.ID, &c.Date, &c.Time, &c.TimeOff,
			&c.FromID, &c.ToID, &c.Band, &c.Mode,
		); err != nil {
			return result, err
		}
		result[c.ID] = c
	}
	return result, rows.Err()
}

// revalidateMatches re-checks existing matches that reference the given
// contacts. An upsert can change a contact's band, mode, or end time, and a
// match made under the old values may no longer hold; those matches are
// deleted. Returns the callsign IDs on either side of deleted matches.
func revalidateMatches(
	ctx context.Context,
	pool *pgxpool.Pool,
	contactIDs []int64,
) ([]int64, error) {
	if len(contactIDs) == 0 {
		return nil, nil
	}

	type matchRow struct {
		id, aID, bID, aCallID, bCallID int64
	}

	rows, err := pool.Query(ctx,
		`SELECT id, a_id, b_id, a_call_id, b_call_id
		 FROM matcher_match WHERE a_id = ANY($1) OR b_id = ANY($1)`,
		contactIDs,
	)
	if err != nil {
		return nil, err
	}

	var candidates []matchRow
	referenced := map[int64]bool{}
	for rows.Next() {
		var m matchRow
		if err := rows.Scan(&m.id, &m.aID, &m.bID, &m.aCallID, &m.bCallID); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, m)
		referenced[m.aID] = true
		referenced[m.bID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	refIDs := make([]int64, 0, len(referenced))
	for id := range referenced {
		refIDs = append(refIDs, id)
	}
	contacts, err := fetchContactsByID(ctx, pool, refIDs)
	if err != nil {
		return nil, err
	}

	var staleIDs []int64
	affected := map[int64]bool{}
	for _, m := range candidates {
		a, okA := contacts[m.aID]
		b, okB := contacts[m.bID]
		if okA && okB && contactsMatch(a, b) {
			continue
		}
		staleIDs = append(staleIDs, m.id)
		affected[m.aCallID] = true
		affected[m.bCallID] = true
	}
	if len(staleIDs) == 0 {
		return nil, nil
	}

	if _, err := pool.Exec(ctx,
		`DELETE FROM matcher_match WHERE id = ANY($1)`, staleIDs,
	); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	return ids, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfEmptySlice(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}
