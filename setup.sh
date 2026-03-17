#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

have_command() {
	command -v "$1" >/dev/null 2>&1
}

parse_go_version() {
	local raw
	raw="$(go env GOVERSION 2>/dev/null || true)"
	if [[ -z "$raw" ]]; then
		raw="$(go version | awk '{print $3}')"
	fi
	raw="${raw#go}"
	echo "$raw"
}

check_go_version() {
	local version major minor
	version="$(parse_go_version)"
	major="${version%%.*}"
	minor="${version#*.}"
	minor="${minor%%.*}"

	if [[ -z "$major" || -z "$minor" ]]; then
		echo "Unable to determine Go version." >&2
		return 1
	fi

	if (( major < MIN_GO_MAJOR )); then
		return 1
	fi
	if (( major == MIN_GO_MAJOR && minor < MIN_GO_MINOR )); then
		return 1
	fi
	return 0
}

echo "Preparing UniFied workspace in $ROOT_DIR"

if ! have_command go; then
	echo "Missing required dependency: go" >&2
	echo "Install Go 1.25+ and rerun ./setup.sh" >&2
	exit 1
fi

if ! check_go_version; then
	echo "Go 1.25+ is required. Found $(parse_go_version)." >&2
	exit 1
fi

mkdir -p "$ROOT_DIR/build" "$ROOT_DIR/data/local" "$ROOT_DIR/logs"

echo "Downloading Go module dependencies"
(cd "$ROOT_DIR" && go mod download)

if have_command npm || have_command npx; then
	echo "Solidity toolchain is available through npx"
else
	echo "Node.js/npx not found; Solidity compilation targets will be unavailable"
fi

cat <<'EOF'
Workspace prepared.

Common commands:
  make test
  make build
  make run-node
  make run-mine
  sudo make install-seed-node

See docs/runbook.md for bootstrap, multi-node, backup, and shutdown guidance.
EOF
