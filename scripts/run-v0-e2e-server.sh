#!/usr/bin/env bash
set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_data_directory="$(mktemp -d "${TMPDIR:-/tmp}/go-workflow-v0-e2e.XXXXXX")"
server_pid=""

cleanup() {
  if [[ -n "${server_pid}" ]]; then
    kill "${server_pid}" 2>/dev/null || true
    wait "${server_pid}" 2>/dev/null || true
  fi
  case "${test_data_directory}" in
    "${TMPDIR:-/tmp}"/go-workflow-v0-e2e.*) rm -rf -- "${test_data_directory}" ;;
    *) printf 'refusing to remove unexpected E2E directory: %s\n' "${test_data_directory}" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM HUP

go build -o "${test_data_directory}/workflow-server" "${root_directory}/cmd/workflow-server"
WORKFLOW_DATA_DIR="${test_data_directory}/data" \
  WORKFLOW_ADDR="${WORKFLOW_E2E_ADDR:-127.0.0.1:55080}" \
  "${test_data_directory}/workflow-server" &
server_pid="$!"
wait "${server_pid}"
server_pid=""
