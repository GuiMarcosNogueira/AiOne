#!/usr/bin/env bash
set -euo pipefail
shopt -s extglob

usage() {
  cat <<'EOF'
Usage: run_with_env.sh [-e|--env-file path] [command ...]
Loads variables from the specified .env file (defaults to .env) and executes the command.
If no command is provided, it runs: go run ./cmd/server
EOF
}

env_file=".env"
if [[ $# -gt 0 ]]; then
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -e|--env-file)
      if [[ $# -lt 2 ]]; then
        echo "error: missing value for $1" >&2
        exit 1
      fi
      env_file="$2"
      shift 2
      ;;
  esac
fi

if [[ $# -eq 0 ]]; then
  set -- go run ./cmd/server
fi

if [[ ! -f "$env_file" ]]; then
  echo "Environment file '$env_file' not found." >&2
  exit 1
fi

trim() {
  local var="$1"
  var="${var##+([[:space:]])}"
  var="${var%%+([[:space:]])}"
  printf '%s' "$var"
}

while IFS= read -r line || [[ -n "$line" ]]; do
  line="$(trim "$line")"
  if [[ -z "$line" || "$line" == \#* ]]; then
    continue
  fi
  if [[ "$line" != *=* ]]; then
    echo "warning: skipping invalid line: $line" >&2
    continue
  fi
  name="${line%%=*}"
  value="${line#*=}"
  name="$(trim "$name")"
  value="$(trim "$value")"
  if [[ -z "$name" ]]; then
    echo "warning: skipping line with empty key" >&2
    continue
  fi
  if [[ ${value} == '"'* && ${value} == *'"' && ${#value} -ge 2 ]]; then
    value="${value:1:-1}"
  elif [[ ${value} == "'*" && ${value} == *"'" && ${#value} -ge 2 ]]; then
    value="${value:1:-1}"
  fi
  export "${name}=${value}"
done < "$env_file"

printf '\033[36mLoaded environment from %s\033[0m\n' "$env_file"
printf '\033[32mRunning command: %s\033[0m\n' "$*"

"$@"
exit_code=$?
exit $exit_code
