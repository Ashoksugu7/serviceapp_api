#!/usr/bin/env bash
# Applies pending database migrations in order and records them in
# schema_migrations. Run from anywhere; needs psql (PostgreSQL 14+).
#
#   DATABASE_URL=postgres://… scripts/migrate.sh status   # show applied/pending
#   DATABASE_URL=postgres://… scripts/migrate.sh up       # apply pending
#   scripts/migrate.sh up --yes                           # no confirmation prompt
#
# Without DATABASE_URL, the standard PG* variables (PGHOST, PGDATABASE, …) are used.
# A database created before this script existed is detected from its tables and
# columns the first time, so already-applied migrations are not run again.
set -euo pipefail

cd "$(dirname "$0")/.."
command=${1:-status}
assume_yes=false
[[ "${2:-}" == "--yes" || "${1:-}" == "--yes" ]] && assume_yes=true
[[ "$command" == "--yes" ]] && command=up

# Name the target database explicitly; psql's fallback (a database named after
# the OS user) could migrate the wrong one. The ${conn[@]+…} form keeps an empty
# array working under `set -u` in macOS bash 3.2.
conn=()
if [[ -n "${DATABASE_URL:-}" ]]; then
  conn=("$DATABASE_URL")
elif [[ -z "${PGDATABASE:-}" ]]; then
  echo "Set DATABASE_URL to the API's database (the same value the API uses), for example:" >&2
  echo "  DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/DBNAME?sslmode=disable' scripts/migrate.sh status" >&2
  exit 1
fi
psql_q() { psql -X -q -t -A -v ON_ERROR_STOP=1 ${conn[@]+"${conn[@]}"} "$@"; }

if ! target=$(psql_q -c "SELECT current_database() || ' on ' || COALESCE(inet_server_addr()::text, 'local socket') || ' as ' || current_user"); then
  echo "Cannot connect to the database. Check DATABASE_URL." >&2
  exit 1
fi
echo "Database: $target"

# Versions already in the database. On first use, infer them from the schema.
detect_baseline() {
  psql_q <<'SQL'
SELECT CASE
  WHEN to_regclass('companies') IS NULL THEN 0
  WHEN to_regclass('service_requests') IS NULL THEN 1
  WHEN to_regclass('auth_sessions') IS NULL THEN 2
  WHEN NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'phone') THEN 3
  WHEN NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'must_change_password') THEN 4
  WHEN to_regclass('password_reset_tokens') IS NULL THEN 5
  WHEN NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'standby_item_issues' AND column_name = 'issued_by') THEN 6
  WHEN EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'customers' AND column_name = 'customer_no') THEN 7
  WHEN EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'companies' AND column_name = 'code') THEN 8
  ELSE 9
END;
SQL
}

if [[ "$(psql_q -c "SELECT to_regclass('schema_migrations') IS NOT NULL")" != "t" ]]; then
  baseline=$(detect_baseline)
  if [[ "$command" == "up" ]]; then
    psql_q -c "CREATE TABLE schema_migrations (version integer PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"
    for file in migrations/[0-9]*.up.sql; do
      name=$(basename "$file" .up.sql); version=$((10#${name%%_*}))
      (( version <= baseline )) && psql_q -c "INSERT INTO schema_migrations(version,name) VALUES ($version,'$name')"
    done
    (( baseline > 0 )) && echo "Recorded existing migrations 1–$baseline (detected from the schema)."
  fi
  applied=""
  (( baseline > 0 )) && applied=$(seq -s ' ' 1 "$baseline")
  if [[ "$command" == "status" ]]; then
    (( baseline > 0 )) && echo "No schema_migrations table yet; detected migrations 1–$baseline from the schema." || echo "Empty database; every migration is pending."
  fi
else
  applied=$(psql_q -c "SELECT string_agg(version::text, ' ' ORDER BY version) FROM schema_migrations")
fi

pending=()
for file in migrations/[0-9]*.up.sql; do
  name=$(basename "$file" .up.sql); version=$((10#${name%%_*}))
  if [[ " $applied " == *" $version "* ]]; then
    [[ "$command" == "status" ]] && echo "applied  $name"
  else
    pending+=("$file")
    [[ "$command" == "status" ]] && echo "pending  $name"
  fi
done

if [[ "$command" == "status" ]]; then
  echo "${#pending[@]} pending."
  exit 0
fi
[[ "$command" != "up" ]] && { echo "Usage: scripts/migrate.sh [status|up] [--yes]" >&2; exit 2; }
(( ${#pending[@]} == 0 )) && { echo "Database is up to date."; exit 0; }

echo "Pending:"; printf '  %s\n' "${pending[@]##*/}"
if [[ " ${pending[*]} " == *"000008_customer_mobile_identity"* && "$(psql_q -c "SELECT to_regclass('customers') IS NOT NULL")" == "t" ]]; then
  duplicates=$(psql_q -c "SELECT count(*) - count(DISTINCT (company_id, regexp_replace(contact,'[^0-9]','','g'))) FROM customers WHERE regexp_replace(contact,'[^0-9]','','g') <> ''")
  echo "000008 merges customers that share a mobile number and deletes the copies: $duplicates customer(s) will be merged. This cannot be undone; back up the database first."
fi
if ! $assume_yes; then
  [[ -t 0 ]] || { echo "Re-run with --yes to apply without a prompt." >&2; exit 1; }
  read -r -p "Apply ${#pending[@]} migration(s)? [y/N] " answer
  [[ "$answer" == [yY]* ]] || { echo "Nothing applied."; exit 1; }
fi

for file in "${pending[@]}"; do
  name=$(basename "$file" .up.sql); version=$((10#${name%%_*}))
  echo "Applying $name"
  # The migration and its record commit together or not at all.
  psql -X -q -v ON_ERROR_STOP=1 --single-transaction ${conn[@]+"${conn[@]}"} \
    -f "$file" -c "INSERT INTO schema_migrations(version,name) VALUES ($version,'$name')"
done
echo "Done. Restart the API so it uses the new schema."
