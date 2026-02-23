#!/usr/bin/env bash

set -e

docker compose down -v
docker compose up -d

# Wait for postgres to be ready
until docker compose exec -T postgres pg_isready -U test -d fast_matcher_test > /dev/null 2>&1; do
  sleep 0.2
done

echo "Database ready."
