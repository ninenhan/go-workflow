#!/usr/bin/env bash

set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_source_directory="${WORKFLOW_WEB_SOURCE_DIR:-"${root_directory}/../go-workflow-react"}"
source "${root_directory}/scripts/v0-common.sh"

v0_validate_build_environment "${web_source_directory}"
v0_build_web "${web_source_directory}"

export WORKFLOW_WEB_DIR="${v0_web_build_directory}"
cd "${root_directory}"
exec go run ./cmd/workflow-server
