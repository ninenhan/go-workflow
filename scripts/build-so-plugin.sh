#!/usr/bin/env sh
set -euo pipefail

# Build a workflow .so plugin into plugins/<goos>-<goarch>/
#
# Usage:
#   ./scripts/build-so-plugin.sh [plugin_pkg] [output_name]
#
# Example:
#   ./scripts/build-so-plugin.sh ./plugins/so/uppercase uppercase

PLUGIN_PKG_INPUT="${1:-./plugins/so/uppercase}"
OUTPUT_NAME="${2:-uppercase}"
GO_BIN="${GO_BIN:-go}"
GCFLAGS_VALUE="${GCFLAGS:-}"
LDFLAGS_VALUE="${LDFLAGS:-}"

GOOS_VALUE="${GOOS:-$("${GO_BIN}" env GOOS)}"
GOARCH_VALUE="${GOARCH:-$("${GO_BIN}" env GOARCH)}"
DEFAULT_GOCACHE="$("${GO_BIN}" env GOCACHE)"
DEFAULT_GOMODCACHE="$("${GO_BIN}" env GOMODCACHE)"
CACHE_GOCACHE="${GOCACHE:-${DEFAULT_GOCACHE}}"
CACHE_GOMODCACHE="${GOMODCACHE:-${DEFAULT_GOMODCACHE}}"

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
# Resolve package path relative to repository root so script works from any cwd.
case "${PLUGIN_PKG_INPUT}" in
/*)
  PLUGIN_PKG="${PLUGIN_PKG_INPUT}"
  ;;
*)
  PLUGIN_PKG="./${PLUGIN_PKG_INPUT#./}"
  ;;
esac
OUT_DIR="${ROOT_DIR}/plugins/${GOOS_VALUE}-${GOARCH_VALUE}"
OUT_FILE="${OUT_DIR}/${OUTPUT_NAME}.so"

mkdir -p "${OUT_DIR}"

echo "Building plugin:"
echo "  package: ${PLUGIN_PKG}"
echo "  target : ${GOOS_VALUE}/${GOARCH_VALUE}"
echo "  output : ${OUT_FILE}"
echo "  go bin : ${GO_BIN}"
if [ -n "${GCFLAGS_VALUE}" ]; then
  echo "  gcflags: ${GCFLAGS_VALUE}"
fi

if [ -n "${GCFLAGS_VALUE}" ] && [ -n "${LDFLAGS_VALUE}" ]; then
  (
    cd "${ROOT_DIR}"
    GOCACHE="${CACHE_GOCACHE}" \
    GOMODCACHE="${CACHE_GOMODCACHE}" \
    GOOS="${GOOS_VALUE}" \
    GOARCH="${GOARCH_VALUE}" \
    "${GO_BIN}" build -buildmode=plugin -o "${OUT_FILE}" -gcflags "${GCFLAGS_VALUE}" -ldflags "${LDFLAGS_VALUE}" "${PLUGIN_PKG}"
  )
elif [ -n "${GCFLAGS_VALUE}" ]; then
  (
    cd "${ROOT_DIR}"
    GOCACHE="${CACHE_GOCACHE}" \
    GOMODCACHE="${CACHE_GOMODCACHE}" \
    GOOS="${GOOS_VALUE}" \
    GOARCH="${GOARCH_VALUE}" \
    "${GO_BIN}" build -buildmode=plugin -o "${OUT_FILE}" -gcflags "${GCFLAGS_VALUE}" "${PLUGIN_PKG}"
  )
elif [ -n "${LDFLAGS_VALUE}" ]; then
  (
    cd "${ROOT_DIR}"
    GOCACHE="${CACHE_GOCACHE}" \
    GOMODCACHE="${CACHE_GOMODCACHE}" \
    GOOS="${GOOS_VALUE}" \
    GOARCH="${GOARCH_VALUE}" \
    "${GO_BIN}" build -buildmode=plugin -o "${OUT_FILE}" -ldflags "${LDFLAGS_VALUE}" "${PLUGIN_PKG}"
  )
else
  (
    cd "${ROOT_DIR}"
    GOCACHE="${CACHE_GOCACHE}" \
    GOMODCACHE="${CACHE_GOMODCACHE}" \
    GOOS="${GOOS_VALUE}" \
    GOARCH="${GOARCH_VALUE}" \
    "${GO_BIN}" build -buildmode=plugin -o "${OUT_FILE}" "${PLUGIN_PKG}"
  )
fi

echo "Done: ${OUT_FILE}"
