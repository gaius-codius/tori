#!/usr/bin/env bash
# Fails when govulncheck finds a vulnerability that the project's code reaches,
# unless its ID is listed in the allowlist (scripts/govulncheck-allow.txt).
#
# Shared from gaius-codius/go-ci (baseline/scripts/govulncheck.sh). Projects keep
# an identical copy at scripts/govulncheck.sh for local runs; CI warns on drift.
# GOVULNCHECK_ROOT and GOVULNCHECK_ALLOW override the module root and allowlist,
# which default to the directory above this script and its allowlist.
set -euo pipefail

repo_root="${GOVULNCHECK_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}"
allow_file="${GOVULNCHECK_ALLOW:-${repo_root}/scripts/govulncheck-allow.txt}"

cd "${repo_root}"

if [[ ! -r "${allow_file}" ]]; then
  echo "Allowlist ${allow_file#"${repo_root}/"} is missing or unreadable." >&2
  exit 1
fi

report="$(govulncheck -format json ./...)"

# Refuse to treat empty, partial, or malformed output as a clean scan.
if ! jq empty <<<"${report}" 2>/dev/null; then
  echo "govulncheck output is not valid JSON; treating the scan as failed." >&2
  exit 1
fi
scanned_go="$(jq -r 'select(.config.scan_level == "symbol") | .config.go_version' <<<"${report}" 2>/dev/null || true)"
if [[ -z "${scanned_go}" ]]; then
  echo "govulncheck produced no symbol-level scan config; treating the scan as failed." >&2
  exit 1
fi
echo "govulncheck: scanned with ${scanned_go}."

# A finding is reachable when its trace starts at a function (symbol level).
reachable="$(jq -r 'select(.finding) | .finding | select(.trace[0].function) | .osv' <<<"${report}" | sort -u)"
allowed="$(sed -e 's/#.*//' -e 's/[[:space:]]//g' "${allow_file}" | grep -v '^$' | sort -u || true)"

blocking="$(comm -23 <(printf '%s\n' "${reachable}" | grep -v '^$' || true) <(printf '%s\n' "${allowed}"))"
stale="$(comm -13 <(printf '%s\n' "${reachable}" | grep -v '^$' || true) <(printf '%s\n' "${allowed}" | grep -v '^$' || true))"

if [[ -n "${stale}" ]]; then
  echo "Allowlisted IDs no longer reachable; remove them from ${allow_file#"${repo_root}/"}:"
  while read -r id; do
    echo "  ${id}"
  done <<<"${stale}"
fi

if [[ -n "${blocking}" ]]; then
  echo "Reachable vulnerabilities:"
  while read -r id; do
    summary="$(jq -r --arg id "${id}" 'select(.osv.id == $id) | .osv.summary' <<<"${report}" | head -1)"
    echo "  ${id}: ${summary} (https://pkg.go.dev/vuln/${id})"
  done <<<"${blocking}"
  echo "Run 'govulncheck ./...' for call traces."
  exit 1
fi

echo "govulncheck: no reachable vulnerabilities outside the allowlist."
