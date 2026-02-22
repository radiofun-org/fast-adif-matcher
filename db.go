package main

import (
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// SQLite 3.32.0+ SQLITE_MAX_VARIABLE_NUMBER default.
const maxParams = 32766

// buildMultiRowSQL builds "PREFIX (?,?,...), (?,?,...), ... SUFFIX" for n rows of cols columns.
func buildMultiRowSQL(prefix string, cols int, n int, suffix string) string {
	placeholder := "(" + strings.Repeat("?,", cols-1) + "?)"
	var b strings.Builder
	b.Grow(len(prefix) + n*(len(placeholder)+1) + len(suffix))
	b.WriteString(prefix)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(placeholder)
	}
	b.WriteString(suffix)
	return b.String()
}

type matchRecord struct {
	MatchedAt, AID, BID, ACallID, BCallID int64
}

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func initDatabase(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS contacts (
			id INTEGER PRIMARY KEY,
			adif_metadata_id INTEGER,
			date INTEGER,
			time INTEGER,
			time_off INTEGER,
			from_id INTEGER,
			to_id INTEGER,
			band INTEGER,
			mode INTEGER,
			sub_mode INTEGER,
			grid INTEGER,
			grid_size INTEGER,
			administrative_primary TEXT,
			administrative_secondary TEXT,
			dxcc INTEGER,
			zone_cq INTEGER,
			zone_itu INTEGER,
			tx_power INTEGER,
			UNIQUE(to_id, date, time, from_id)
		)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS adif_metadata (
			id integer PRIMARY KEY,
			process_time INTEGER,
			file_hash TEXT,
			adif_version TEXT,
			program_id TEXT,
			program_version TEXT,
			UNIQUE(file_hash) ON CONFLICT IGNORE
		)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS matches (
			id integer PRIMARY KEY,
			matched_at INTEGER,
			a_id INTEGER,
			b_id INTEGER,
			a_call_id INTEGER,
			b_call_id INTEGER,
			UNIQUE (a_id, b_id) ON CONFLICT IGNORE
		)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS callsigns (
			id integer PRIMARY KEY,
			callsign text unique
		)`)
	if err != nil {
		return err
	}

	return nil
}

func addCallsign(db *sql.DB, callsign string) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO callsigns (callsign) VALUES (?)
		 ON CONFLICT(callsign) DO UPDATE SET callsign=callsign
		 RETURNING id`,
		callsign,
	).Scan(&id)
	return id, err
}

func insertAdifMetadata(
	db *sql.DB,
	processTime int64,
	fileHash, adifVersion, programId, programVersion string,
) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO adif_metadata (process_time, file_hash, adif_version, program_id, program_version)
		 VALUES (?, ?, ?, ?, ?)
		 RETURNING id`,
		processTime,
		fileHash,
		adifVersion,
		programId,
		programVersion,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func queryCallsignsByJSON(db *sql.DB, callsJSON []byte) (map[string]int64, error) {
	result := map[string]int64{}
	rows, err := db.Query(
		`SELECT id, callsign FROM callsigns WHERE callsign IN (SELECT value FROM json_each(?))`,
		string(callsJSON),
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

func bulkInsertCallsigns(
	db *sql.DB,
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

	const chunk = maxParams // 1 param per row

	tx, err := db.Begin()
	if err != nil {
		return existing, err
	}
	defer tx.Rollback()

	const prefix = `INSERT INTO callsigns (callsign) VALUES `
	const suffix = ` ON CONFLICT(callsign) DO UPDATE SET callsign=callsign RETURNING id, callsign`

	// precompute SQL for full-sized chunks
	fullSQL := buildMultiRowSQL(prefix, 1, chunk, suffix)

	for i := 0; i < len(newCalls); i += chunk {
		end := i + chunk
		if end > len(newCalls) {
			end = len(newCalls)
		}
		batch := newCalls[i:end]

		query := fullSQL
		if len(batch) < chunk {
			query = buildMultiRowSQL(prefix, 1, len(batch), suffix)
		}

		args := make([]any, len(batch))
		for j, call := range batch {
			args[j] = call
		}

		rows, err := tx.Query(query, args...)
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
			rows.Close()
			return existing, err
		}
		rows.Close()
	}

	if err := tx.Commit(); err != nil {
		return existing, err
	}
	return existing, nil
}

func bulkInsertContacts(db *sql.DB, contacts []Contact, adifMetadataId int64) error {
	if len(contacts) == 0 {
		return nil
	}

	const cols = 17
	const chunk = maxParams / cols

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const prefix = `INSERT INTO contacts (adif_metadata_id, date, time, time_off, from_id, to_id, band, mode, sub_mode, grid, grid_size, administrative_primary, administrative_secondary, dxcc, zone_cq, zone_itu, tx_power) VALUES `
	const suffix = ` ON CONFLICT(to_id, date, time, from_id) DO UPDATE SET adif_metadata_id=excluded.adif_metadata_id, time_off=excluded.time_off, band=excluded.band, mode=excluded.mode, grid=excluded.grid, grid_size=excluded.grid_size, administrative_primary=excluded.administrative_primary, administrative_secondary=excluded.administrative_secondary, dxcc=excluded.dxcc, zone_cq=excluded.zone_cq, zone_itu=excluded.zone_itu, tx_power=excluded.tx_power RETURNING id`

	fullSQL := buildMultiRowSQL(prefix, cols, chunk, suffix)

	for i := 0; i < len(contacts); i += chunk {
		end := i + chunk
		if end > len(contacts) {
			end = len(contacts)
		}
		batch := contacts[i:end]

		query := fullSQL
		if len(batch) < chunk {
			query = buildMultiRowSQL(prefix, cols, len(batch), suffix)
		}

		args := make([]any, 0, len(batch)*cols)
		for _, c := range batch {
			args = append(
				args,
				adifMetadataId,
				c.Date,
				c.Time,
				c.TimeOff,
				c.FromID,
				c.ToID,
				c.Band,
				c.Mode,
				c.SubMode,
				c.Grid,
				c.GridSize,
				c.AdministrativePrimary,
				c.AdministrativeSecondary,
				c.DXCC,
				c.ZoneCQ,
				c.ZoneITU,
				c.TransmitPower,
			)
		}

		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		for j := 0; rows.Next(); j++ {
			if err := rows.Scan(&contacts[i+j].ID); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	return tx.Commit()
}

func findPotentialMatches(
	db *sql.DB,
	fromId int64,
	dateStart int,
	dateEnd int,
) ([]Contact, error) {
	var contacts []Contact

	rows, err := db.Query(
		`SELECT id, date, time, time_off, from_id, to_id, band, mode, grid, grid_size
		 FROM contacts
		 WHERE to_id = ? AND date >= ? AND date <= ?`,
		fromId, dateStart, dateEnd,
	)
	if err != nil {
		return contacts, err
	}
	defer rows.Close()

	for rows.Next() {
		var c Contact
		if err := rows.Scan(
			&c.ID,
			&c.Date,
			&c.Time,
			&c.TimeOff,
			&c.FromID,
			&c.ToID,
			&c.Band,
			&c.Mode,
			&c.Grid,
			&c.GridSize,
		); err != nil {
			return contacts, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func bulkInsertMatches(db *sql.DB, matches []matchRecord) error {
	if len(matches) == 0 {
		return nil
	}

	const cols = 5
	const chunk = maxParams / cols

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const prefix = `INSERT INTO matches (matched_at, a_id, b_id, a_call_id, b_call_id) VALUES `
	fullSQL := buildMultiRowSQL(prefix, cols, chunk, "")

	for i := 0; i < len(matches); i += chunk {
		end := i + chunk
		if end > len(matches) {
			end = len(matches)
		}
		batch := matches[i:end]

		query := fullSQL
		if len(batch) < chunk {
			query = buildMultiRowSQL(prefix, cols, len(batch), "")
		}

		args := make([]any, 0, len(batch)*cols)
		for _, m := range batch {
			args = append(args, m.MatchedAt, m.AID, m.BID, m.ACallID, m.BCallID)
		}

		_, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
