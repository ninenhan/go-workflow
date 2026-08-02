#!/usr/bin/env bash

v0_fail() {
  printf '%s\n' "$1" >&2
  return 1
}

v0_require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    v0_fail "required command is unavailable: $1"
  fi
}

v0_validate_build_environment() {
  local web_source_directory="$1"
  local command_name

  for command_name in go node pnpm; do
    v0_require_command "${command_name}"
  done

  if [[ ! -f "${web_source_directory}/package.json" ||
    ! -f "${web_source_directory}/pnpm-lock.yaml" ||
    ! -f "${web_source_directory}/pnpm-workspace.yaml" ]]; then
    printf 'set WORKFLOW_WEB_SOURCE_DIR to the go-workflow-react directory\n' >&2
    v0_fail "workflow web source is invalid: ${web_source_directory}"
  fi

  local node_major_version
  node_major_version="$(node -p 'process.versions.node.split(".")[0]')"
  if ((node_major_version < 18)); then
    v0_fail "Node.js 18 or newer is required; found $(node --version)"
  fi

  local required_pnpm_version="10.28.0"
  local current_pnpm_version
  current_pnpm_version="$(pnpm --version)"
  if [[ "${current_pnpm_version}" != "${required_pnpm_version}" ]]; then
    v0_fail "pnpm ${required_pnpm_version} is required; found ${current_pnpm_version}"
  fi
}

v0_install_web_dependencies() {
  local web_source_directory="$1"

  pnpm --dir "${web_source_directory}" install --frozen-lockfile
}

v0_assert_web_build() {
  local web_source_directory="$1"

  v0_web_build_directory="${web_source_directory}/dist"
  if [[ ! -f "${v0_web_build_directory}/index.html" ||
    ! -d "${v0_web_build_directory}/assets" ]]; then
    v0_fail "workflow web production build is incomplete: ${v0_web_build_directory}"
  fi
}

v0_build_web() {
  local web_source_directory="$1"

  v0_install_web_dependencies "${web_source_directory}"
  pnpm --dir "${web_source_directory}" build
  v0_assert_web_build "${web_source_directory}"
}

v0_run_release_gates() {
  local root_directory="$1"
  local web_source_directory="$2"

  v0_install_web_dependencies "${web_source_directory}"
  (
    cd "${root_directory}"
    go test ./...
  )
  pnpm --dir "${web_source_directory}" check:v0
  node "${root_directory}/scripts/verify-web-runtime-contract.mjs" \
    "${web_source_directory}"
  v0_assert_web_build "${web_source_directory}"
}
