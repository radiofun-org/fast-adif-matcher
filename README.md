# fast-adif-matcher

The matching engine for [radio.games](https://radio.games/). A Go service that parses uploaded ADIF logs, stores contacts in PostgreSQL, and finds confirmed mutual contacts (both operators logged the same QSO).

The [website](https://github.com/radiofun-org/radio-games-website) enqueues uploads to the Redis list `log_upload_queue`; this service consumes them, writes to the `matcher_*` tables (which it creates itself), and adds affected callsign IDs to the Redis set `awards_eval_pending` for the website's awards worker. On startup it re-processes any uploads whose processing was interrupted (`processed = false` in `app_logupload`). Callsigns are normalized (uppercased, trimmed) at ingest, and matches are stored with `a_id < b_id` so the same QSO discovered from either side dedupes.

Corrections flow through two mechanisms. Existing matches on upserted contacts are re-checked after every upload, so a corrected band or mode withdraws a confirmation it invalidates. An upload with `replaces_id` set is a corrected re-upload of a specific earlier log: the two files are diffed, and contacts only the old file asserted (a wrong callsign, date, or time) are deleted along with their matches. Affected callsigns are queued for award re-evaluation either way.

## Matching rules

Two contacts match when all three hold:

- **Same band** (inferred from frequency when missing)
- **Within 30 minutes** — time ranges (`TIME_ON`..`TIME_OFF`, midnight crossings handled) overlap or come within 30 minutes
- **Same mode or mode group** (DATA / PHONE / CW / IMAGE)

Missing fields are inferred where possible: mode from submode, DXCC from country, CQ/ITU zones from DXCC or state, grid square from latitude/longitude, and POTA/SOTA/WWFF/IOTA references from `MY_SIG`/`MY_SIG_INFO`.

## Configuration

Environment variables (a `.env` file is loaded automatically): `DATABASE_URL`, `REDIS_URL`, `UPLOAD_DIR`, `SENTRY_DSN` (optional).

## Running

```bash
go build
./fast-matcher                                   # consume the Redis queue
./fast-matcher -filename log.adi -callsign N0CALL  # import one file directly
```

`-profile-cpu <file>` writes a CPU profile.

## Testing

```bash
go test ./...        # unit + integration tests (integration uses testcontainers)
docker compose up -d # Postgres on :5433 for the shell scripts
./test.sh            # build, import sample logs from tmp/, print matches
```

`adif-spec/` holds the ADIF 3.1.7 specification the field handling is based on.
