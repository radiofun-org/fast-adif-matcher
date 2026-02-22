package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type matchRecord struct {
	MatchedAt, AID, BID, ACallID, BCallID int64
}

func openPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	return pgxpool.NewWithConfig(ctx, cfg)
}

func initDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS contacts (
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
			my_pota TEXT,
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

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS adif_metadata (
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
		CREATE TABLE IF NOT EXISTS matches (
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
		CREATE TABLE IF NOT EXISTS callsigns (
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
		`INSERT INTO callsigns (callsign) VALUES ($1)
		 ON CONFLICT(callsign) DO UPDATE SET callsign=callsigns.callsign
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
		`INSERT INTO adif_metadata (process_time, file_hash, adif_version, program_id, program_version)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (file_hash) DO NOTHING
		 RETURNING id`,
		processTime,
		fileHash,
		adifVersion,
		programId,
		programVersion,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func queryCallsigns(
	ctx context.Context,
	pool *pgxpool.Pool,
	callsigns []string,
) (map[string]int64, error) {
	result := map[string]int64{}
	rows, err := pool.Query(ctx,
		`SELECT id, callsign FROM callsigns WHERE callsign = ANY($1)`,
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
		`INSERT INTO callsigns (callsign)
		 SELECT callsign FROM _tmp_callsigns
		 ON CONFLICT(callsign) DO UPDATE SET callsign=callsigns.callsign
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
		my_pota TEXT, my_sota TEXT, my_wwff TEXT, my_iota TEXT,
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
				nilIfEmpty(c.MyPOTA), nilIfEmpty(c.MySOTA),
				nilIfEmpty(c.MyWWFF), nilIfEmpty(c.MyIOTA),
				nilIfEmpty(c.MySig), nilIfEmpty(c.MySigInfo),
			}, nil
		}),
	)
	if err != nil {
		return err
	}

	rows, err := tx.Query(ctx,
		`WITH ins AS (
			INSERT INTO contacts (
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
				my_pota=COALESCE(excluded.my_pota,contacts.my_pota),
				my_sota=COALESCE(excluded.my_sota,contacts.my_sota),
				my_wwff=COALESCE(excluded.my_wwff,contacts.my_wwff),
				my_iota=COALESCE(excluded.my_iota,contacts.my_iota),
				my_sig=COALESCE(excluded.my_sig,contacts.my_sig),
				my_sig_info=COALESCE(excluded.my_sig_info,contacts.my_sig_info)
			RETURNING contacts.id, contacts.to_id, contacts.date, contacts.time, contacts.from_id
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
		 FROM contacts
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
		`INSERT INTO matches (matched_at, a_id, b_id, a_call_id, b_call_id)
		 SELECT matched_at, a_id, b_id, a_call_id, b_call_id FROM _tmp_matches
		 ON CONFLICT (a_id, b_id) DO NOTHING`)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
