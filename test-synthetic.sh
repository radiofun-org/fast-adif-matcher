#!/usr/bin/env bash

rm ./contacts.sqlite

go build

echo

for FILE in ../generate-adifs/adifs/*.adif; do
  CALL=$(basename "$FILE" .adif)
  ./fast-matcher -filename "$FILE" -callsign "$CALL"
done
