#!/bin/sh
set -eu

deployment_dir=${STUTRADE_DEPLOY_DIR:-/opt/stu-trade}
compose_file=$deployment_dir/compose.production.yaml
environment_file=$deployment_dir/.env
lock_directory=$deployment_dir/deploy.lock

if ! mkdir "$lock_directory" 2>/dev/null; then
  echo "another StuTrade deployment is running" >&2
  exit 1
fi
trap 'rmdir "$lock_directory"' EXIT

if [ "$#" -ne 2 ]; then
  echo "usage: deploy.sh <backend-image> <web-image>" >&2
  exit 2
fi

backend_image=$1
web_image=$2
compose() {
  BACKEND_IMAGE=$backend_image WEB_IMAGE=$web_image \
    docker compose --env-file "$environment_file" -f "$compose_file" "$@"
}

compose pull postgres backend web
compose up -d postgres

backup_file=$deployment_dir/backups/postgres-$(date -u +%Y%m%dT%H%M%SZ).sql.gz
compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip -9 >"$backup_file"

compose run --rm backend migrate up --profile=production
compose up -d --remove-orphans

curl --fail --silent --show-error --retry 20 --retry-delay 2 --retry-all-errors \
  http://127.0.0.1:8081/ready >/dev/null
curl --fail --silent --show-error --retry 10 --retry-delay 2 --retry-all-errors \
  http://127.0.0.1:8081/ >/dev/null

printf '%s\n' "$backend_image" >"$deployment_dir/current-backend-image"
printf '%s\n' "$web_image" >"$deployment_dir/current-web-image"
