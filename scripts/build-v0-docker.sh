#!/usr/bin/env bash

set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_source_directory="${WORKFLOW_WEB_SOURCE_DIR:-"${root_directory}/../go-workflow-react"}"
dockerfile="${root_directory}/deploy/docker/Dockerfile"
source "${root_directory}/scripts/v0-common.sh"

v0_validate_build_environment "${web_source_directory}"
for command_name in docker install mktemp; do
  v0_require_command "${command_name}"
done
v0_build_web "${web_source_directory}"

target_architecture="${V0_DOCKER_ARCH:-amd64}"
case "${target_architecture}" in
amd64 | arm64) ;;
*) v0_fail "unsupported Docker architecture: ${target_architecture}" ;;
esac

version="$(node -p 'require(process.argv[1]).version' "${web_source_directory}/package.json")"
image_tag="${V0_DOCKER_TAG:-go-workflow-v0:${version}}"
staging_directory="$(mktemp -d "${TMPDIR:-/tmp}/go-workflow-v0-docker.XXXXXX")"
trap 'rm -rf "${staging_directory}"' EXIT

CGO_ENABLED=0 GOOS=linux GOARCH="${target_architecture}" go build \
  -trimpath \
  -buildvcs=false \
  -ldflags "-s -w -X main.buildVersion=${version}" \
  -o "${staging_directory}/workflow-server" \
  ./cmd/workflow-server
install -d "${staging_directory}/web"
cp -R "${web_source_directory}/dist/." "${staging_directory}/web/"

docker build \
  --platform "linux/${target_architecture}" \
  --file "${dockerfile}" \
  --tag "${image_tag}" \
  "${staging_directory}"

printf 'V0 Docker image: %s\n' "${image_tag}"
