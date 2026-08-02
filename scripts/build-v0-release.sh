#!/usr/bin/env bash

set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_source_directory="${WORKFLOW_WEB_SOURCE_DIR:-"${root_directory}/../go-workflow-react"}"
output_directory="${V0_OUTPUT_DIR:-"${root_directory}/dist/v0"}"
source "${root_directory}/scripts/v0-common.sh"

v0_checksum() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
    return
  fi
  v0_fail "SHA-256 command is unavailable"
}

v0_build_time() {
  if [[ -n "${SOURCE_DATE_EPOCH:-}" ]]; then
    node -e '
      const seconds = Number(process.argv[1]);
      if (!Number.isInteger(seconds) || seconds < 0) process.exit(1);
      process.stdout.write(new Date(seconds * 1000).toISOString().replace(".000", ""));
    ' "${SOURCE_DATE_EPOCH}" ||
      v0_fail "SOURCE_DATE_EPOCH must be a non-negative integer"
    return
  fi
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

target_specs=("$@")
if ((${#target_specs[@]} == 0)) && [[ -n "${V0_TARGETS:-}" ]]; then
  read -r -a target_specs <<<"${V0_TARGETS}"
fi
if ((${#target_specs[@]} == 0)); then
  target_specs=("$(go env GOOS)/$(go env GOARCH)")
fi

validated_targets=()
for target_spec in "${target_specs[@]}"; do
  if [[ ! "${target_spec}" =~ ^(darwin|windows|linux)/(amd64|arm64)$ ]]; then
    v0_fail "unsupported desktop release target: ${target_spec}"
  fi
  if ((${#validated_targets[@]} > 0)) &&
    [[ " ${validated_targets[*]} " == *" ${target_spec} "* ]]; then
    v0_fail "duplicate desktop release target: ${target_spec}"
  fi
  validated_targets+=("${target_spec}")
done

v0_validate_build_environment "${web_source_directory}"
for command_name in cmp find install tar; do
  v0_require_command "${command_name}"
done
v0_run_release_gates "${root_directory}" "${web_source_directory}"

version="$(node -p 'require(process.argv[1]).version' "${web_source_directory}/package.json")"
if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  v0_fail "invalid V0 package version: ${version}"
fi

host_goos="$(go env GOHOSTOS)"
host_goarch="$(go env GOHOSTARCH)"
revision="$(git -C "${root_directory}" rev-parse HEAD)"
if [[ -n "$(git -C "${root_directory}" status --porcelain)" ]]; then
  dirty=true
else
  dirty=false
fi
build_time="$(v0_build_time)"
ldflags="-s -w -X main.buildVersion=${version} -X main.buildRevision=${revision} -X main.buildTime=${build_time}"

staging_root="$(mktemp -d "${TMPDIR:-/tmp}/go-workflow-v0-release.XXXXXX")"
trap 'rm -rf "${staging_root}"' EXIT
runtime_probe="${staging_root}/workflow-server-runtime-probe"
runtime_manifest="${staging_root}/RUNTIME_UNITS.json"
CGO_ENABLED=0 GOOS="${host_goos}" GOARCH="${host_goarch}" go build \
  -trimpath \
  -buildvcs=false \
  -ldflags "${ldflags}" \
  -o "${runtime_probe}" \
  ./cmd/workflow-server
"${runtime_probe}" --runtime-units >"${runtime_manifest}"

mkdir -p "${output_directory}"
for target_spec in "${validated_targets[@]}"; do
  goos="${target_spec%/*}"
  goarch="${target_spec#*/}"
  package_name="go-workflow-v0-${version}-${goos}-${goarch}"
  package_directory="${staging_root}/${package_name}"
  final_directory="${output_directory}/${package_name}"
  archive_path="${output_directory}/${package_name}.tar.gz"

  if [[ "${goos}" == "windows" ]]; then
    binary_name="workflow-server.exe"
    launcher_name="run.cmd"
  else
    binary_name="workflow-server"
    launcher_name="run.sh"
  fi
  if [[ "${goos}/${goarch}" == "${host_goos}/${host_goarch}" ]]; then
    verification_mode="native"
  else
    verification_mode="static"
  fi

  mkdir -p "${package_directory}/bin" "${package_directory}/web"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build \
    -trimpath \
    -buildvcs=false \
    -ldflags "${ldflags}" \
    -o "${package_directory}/bin/${binary_name}" \
    ./cmd/workflow-server
  cp "${runtime_manifest}" "${package_directory}/RUNTIME_UNITS.json"
  cp -R "${v0_web_build_directory}/." "${package_directory}/web/"
  if [[ "${goos}" == "windows" ]]; then
    install -m 644 "${root_directory}/scripts/v0-package-run.cmd" \
      "${package_directory}/${launcher_name}"
  else
    install -m 755 "${root_directory}/scripts/v0-package-run.sh" \
      "${package_directory}/${launcher_name}"
  fi
  install -m 644 "${root_directory}/docs/v0-package-readme.md" \
    "${package_directory}/README.md"
  install -m 644 "${root_directory}/LICENSE" "${package_directory}/LICENSE"

  printf '{\n  "name": "go-workflow-v0",\n  "version": "%s",\n  "platform": "%s",\n  "architecture": "%s",\n  "source_revision": "%s",\n  "source_dirty": %s,\n  "built_at": "%s",\n  "go_version": "%s",\n  "node_version": "%s",\n  "pnpm_version": "%s",\n  "cgo_enabled": false,\n  "verification": "%s"\n}\n' \
    "${version}" \
    "${goos}" \
    "${goarch}" \
    "${revision}" \
    "${dirty}" \
    "${build_time}" \
    "$(go env GOVERSION)" \
    "$(node --version)" \
    "$(pnpm --version)" \
    "${verification_mode}" \
    >"${package_directory}/RELEASE.json"

  (
    cd "${package_directory}"
    while IFS= read -r file_path; do
      v0_checksum "${file_path}"
    done < <(find . -type f ! -name SHA256SUMS | LC_ALL=C sort)
  ) >"${package_directory}/SHA256SUMS"

  rm -rf "${final_directory}"
  rm -f "${archive_path}" "${archive_path}.sha256"
  mv "${package_directory}" "${final_directory}"
  tar -czf "${archive_path}" -C "${output_directory}" "${package_name}"
  (
    cd "${output_directory}"
    v0_checksum "$(basename "${archive_path}")"
  ) >"${archive_path}.sha256"

  verifier_arguments=(
    "${root_directory}/scripts/verify-v0-release.mjs"
    "${archive_path}"
    "${web_source_directory}"
  )
  if [[ "${verification_mode}" == "native" ]]; then
    version_output="$("${final_directory}/bin/${binary_name}" --version)"
    if [[ "${version_output}" != "workflow-server ${version} ("* ]]; then
      v0_fail "packaged binary version is invalid: ${version_output}"
    fi
    "${final_directory}/bin/${binary_name}" --runtime-units \
      >"${staging_root}/target-runtime-units.json"
    if ! cmp -s "${runtime_manifest}" "${staging_root}/target-runtime-units.json"; then
      v0_fail "packaged runtime unit manifest does not match ${target_spec}"
    fi
  else
    verifier_arguments+=(--static)
  fi
  node "${verifier_arguments[@]}"

  printf 'V0 package directory: %s\n' "${final_directory}"
  printf 'V0 package archive: %s\n' "${archive_path}"
  printf 'V0 archive checksum: %s\n' "${archive_path}.sha256"
done
