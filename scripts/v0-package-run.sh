#!/usr/bin/env bash

set -euo pipefail

package_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$(uname -s)" in
Darwin)
  default_data_directory="${HOME}/Library/Application Support/go-workflow"
  ;;
Linux)
  default_data_directory="${XDG_DATA_HOME:-"${HOME}/.local/share"}/go-workflow"
  ;;
*)
  printf 'unsupported V0 package host: %s\n' "$(uname -s)" >&2
  exit 1
  ;;
esac

umask 077
export WORKFLOW_WEB_DIR="${package_directory}/web"
export WORKFLOW_DATA_DIR="${WORKFLOW_DATA_DIR:-"${default_data_directory}"}"
exec "${package_directory}/bin/workflow-server" "$@"
