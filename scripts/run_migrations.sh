#!/bin/sh
set -e

if [ -z "$DATABASE_URL" ]; then
  echo "DATABASE_URL is required" >&2
  exit 1
fi

for file in /migrations/*.sql; do
  if [ ! -f "$file" ]; then
    continue
  fi

  echo "Applying $file"

  tmp_file=$(mktemp)
  awk '
    BEGIN { in_up = 0 }
    /^--[[:space:]]*\+migrate[[:space:]]+Up/ { in_up = 1; next }
    /^--[[:space:]]*\+migrate[[:space:]]+Down/ { in_up = 0; next }
    { if (in_up) print }
  ' "$file" > "$tmp_file"

  if [ ! -s "$tmp_file" ]; then
    # No migrate markers found; fallback to running the whole file
    cat "$file" > "$tmp_file"
  fi

  psql -v ON_ERROR_STOP=1 "$DATABASE_URL" -f "$tmp_file"
  rm -f "$tmp_file"
done
