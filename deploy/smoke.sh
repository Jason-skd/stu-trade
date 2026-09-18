#!/bin/sh
set -eu

if [ -z "${BACKEND_IMAGE:-}" ] || [ -z "${WEB_IMAGE:-}" ]; then
  echo "BACKEND_IMAGE and WEB_IMAGE are required" >&2
  exit 2
fi

project=stu-trade-smoke
compose_file=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/compose.production.yaml

export POSTGRES_DB=stutrade_smoke
export POSTGRES_USER=stutrade
export POSTGRES_PASSWORD=stutrade_smoke
export STUTRADE_DATABASE_URL='postgres://stutrade:stutrade_smoke@postgres:5432/stutrade_smoke?sslmode=disable'

compose() {
  docker compose -p "$project" -f "$compose_file" "$@"
}

cleanup() {
  compose down --volumes --remove-orphans
}
trap cleanup EXIT

compose up -d postgres
compose run --rm backend migrate up --profile=production
compose up -d --remove-orphans

curl --fail --silent --show-error --retry 30 --retry-delay 2 --retry-all-errors \
  http://127.0.0.1:8081/ready >/dev/null
curl --fail --silent --show-error --retry 10 --retry-delay 2 --retry-all-errors \
  http://127.0.0.1:8081/ >/dev/null
