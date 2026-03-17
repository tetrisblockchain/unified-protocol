#!/usr/bin/env bash

set -euo pipefail

GO_VERSION="${UNIFIED_GO_VERSION:-1.26.1}"
INSTALL_ROOT="${UNIFIED_GO_INSTALL_ROOT:-/usr/local}"
GO_ROOT="${INSTALL_ROOT}/go"
PROFILE_FILE="${UNIFIED_GO_PROFILE_FILE:-/etc/profile.d/unified-go.sh}"
DRY_RUN="${UNIFIED_DRY_RUN:-0}"

usage() {
	cat <<'EOF'
Install Go on a Linux host using the official go.dev tarball.

Usage:
  sudo ./scripts/ops/install_go_linux.sh [--dry-run]

Environment overrides:
  UNIFIED_GO_VERSION         Go version to install. Default: 1.26.1
  UNIFIED_GO_INSTALL_ROOT    Installation root. Default: /usr/local
  UNIFIED_GO_PROFILE_FILE    Shell profile snippet. Default: /etc/profile.d/unified-go.sh
  UNIFIED_DRY_RUN            Set to 1 for a simulated run.
EOF
}

log() {
	echo "[install-go-linux] $*"
}

run_cmd() {
	if [[ "$DRY_RUN" == "1" ]]; then
		printf '[dry-run] '
		printf '%q ' "$@"
		printf '\n'
		return 0
	fi
	"$@"
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

detect_arch() {
	case "$(uname -m)" in
	x86_64|amd64)
		echo "amd64"
		;;
	aarch64|arm64)
		echo "arm64"
		;;
	*)
		echo "Unsupported Linux architecture: $(uname -m)" >&2
		exit 1
		;;
	esac
}

write_profile() {
	run_cmd install -d -m 0755 "$(dirname "$PROFILE_FILE")"
	if [[ "$DRY_RUN" == "1" ]]; then
		echo "[dry-run] write $PROFILE_FILE"
		return 0
	fi
	cat >"$PROFILE_FILE" <<EOF
export PATH="${GO_ROOT}/bin:\$PATH"
EOF
	chmod 0644 "$PROFILE_FILE"
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--dry-run)
		DRY_RUN=1
		shift
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "Unknown argument: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "This installer only supports Linux hosts." >&2
	exit 1
fi

if [[ "$DRY_RUN" != "1" && "${EUID}" -ne 0 ]]; then
	echo "Run this installer as root or through sudo." >&2
	exit 1
fi

require_command curl
require_command tar
require_command install

ARCH="$(detect_arch)"
ARCHIVE="go${GO_VERSION}.linux-${ARCH}.tar.gz"
DOWNLOAD_URL="https://go.dev/dl/${ARCHIVE}"
TMPDIR="$(mktemp -d)"
ARCHIVE_PATH="${TMPDIR}/${ARCHIVE}"
trap 'rm -rf "$TMPDIR"' EXIT

log "downloading ${DOWNLOAD_URL}"
run_cmd curl -fsSL "$DOWNLOAD_URL" -o "$ARCHIVE_PATH"

log "installing Go ${GO_VERSION} to ${GO_ROOT}"
if [[ "$DRY_RUN" == "1" ]]; then
	echo "[dry-run] rm -rf ${GO_ROOT}"
else
	rm -rf "$GO_ROOT"
fi
run_cmd tar -C "$INSTALL_ROOT" -xzf "$ARCHIVE_PATH"

log "writing ${PROFILE_FILE}"
write_profile

cat <<EOF

Go installation complete.

Installed:
  Version: ${GO_VERSION}
  GOROOT:  ${GO_ROOT}
  PATH:    ${GO_ROOT}/bin

Next steps:
  1. Reload your shell:
       source ${PROFILE_FILE}
  2. Verify:
       go version
  3. Rerun the seed-node installer:
       sudo ./scripts/ops/install_seed_node.sh
EOF
