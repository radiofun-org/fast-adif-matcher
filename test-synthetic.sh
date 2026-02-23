#!/usr/bin/env bash

set -e

export DATABASE_URL="${DATABASE_URL:-postgres://test:test@localhost:5433/fast_matcher_test}"

./clear-db.sh

go build

echo

for FILE in ../generate-adifs/adifs/*.adif; do
  CALL=$(basename "$FILE" .adif)
  ./fast-matcher -filename "$FILE" -callsign "$CALL"
done
