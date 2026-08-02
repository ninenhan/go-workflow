#!/usr/bin/env bash

set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "${root_directory}/scripts/build-v0-release.sh" \
  darwin/arm64 \
  windows/amd64 \
  linux/amd64
