#!/usr/bin/env bash
set -euo pipefail

export LC_ALL=C
export LANG=C

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

GO_BIN="${GO:-go}"
NPM_BIN="${NPM:-npm}"
SOLC_BIN="${SOLC:-npx --yes solc@0.8.24}"

RELEASE_VERSION="${RELEASE_VERSION:-mainnet-$(date -u +%Y%m%d-%H%M%S)}"
RELEASE_OUT="${RELEASE_OUT:-$ROOT_DIR/build/release/$RELEASE_VERSION}"
RELEASE_NETWORK_CONFIG="${RELEASE_NETWORK_CONFIG:-$ROOT_DIR/config/networks/unified-network.mainnet.json}"
RELEASE_TARGETS="${RELEASE_TARGETS:-linux/amd64 linux/arm64 darwin/arm64}"
RELEASE_INCLUDE_DESKTOP="${RELEASE_INCLUDE_DESKTOP:-1}"
RELEASE_INCLUDE_DESKTOP_NODE_MODULES="${RELEASE_INCLUDE_DESKTOP_NODE_MODULES:-0}"
RELEASE_INCLUDE_WEBSITE="${RELEASE_INCLUDE_WEBSITE:-1}"
RELEASE_INCLUDE_CONTRACTS="${RELEASE_INCLUDE_CONTRACTS:-1}"

ARTIFACTS_DIR="$RELEASE_OUT/artifacts"
MANIFEST_PATH="$RELEASE_OUT/release-manifest.json"
CHECKSUMS_PATH="$RELEASE_OUT/SHA256SUMS"
SUMMARY_PATH="$RELEASE_OUT/RELEASE_NOTES.txt"

log() {
  printf '[package-mainnet-release] %s\n' "$*"
}

fail() {
  printf '[package-mainnet-release] error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required dependency: $1"
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    fail "missing checksum utility (shasum or sha256sum)"
  fi
}

append_checksum() {
  local file="$1"
  local rel
  rel="$(basename "$file")"
  printf '%s  %s\n' "$(sha256_file "$file")" "$rel" >> "$CHECKSUMS_PATH"
}

host_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
  esac
  printf '%s-%s' "$os" "$arch"
}

copy_common_runtime_files() {
  local target_dir="$1"
  mkdir -p "$target_dir/config" "$target_dir/docs" "$target_dir/deploy" "$target_dir/scripts/ops"

  cp "$RELEASE_NETWORK_CONFIG" "$target_dir/config/unified-network.mainnet.json"
  cp "$ROOT_DIR/README.md" "$target_dir/README.md"
  cp "$ROOT_DIR/docs/runbook.md" "$target_dir/docs/runbook.md"
  cp -R "$ROOT_DIR/deploy/." "$target_dir/deploy/"
  cp -R "$ROOT_DIR/scripts/ops/." "$target_dir/scripts/ops/"
  cp "$ROOT_DIR/setup.sh" "$target_dir/setup.sh"
}

package_runtime_bundle() {
  local target="$1"
  local goos="${target%/*}"
  local goarch="${target#*/}"
  local bundle_name="unified-runtime-${RELEASE_VERSION}-${goos}-${goarch}"
  local stage_dir="$TMP_DIR/$bundle_name"
  local artifact_path="$ARTIFACTS_DIR/${bundle_name}.tar.gz"

  log "building runtime bundle for ${goos}/${goarch}"
  rm -rf "$stage_dir"
  mkdir -p "$stage_dir/bin"
  copy_common_runtime_files "$stage_dir"

  (
    cd "$ROOT_DIR"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$stage_dir/bin/unified-node" ./cmd/unified-node
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$stage_dir/bin/unified-cli" ./cmd/unified-cli
  )

  cat > "$stage_dir/RELEASE.txt" <<EOF
UniFied Mainnet Runtime Bundle
Version: $RELEASE_VERSION
Target: $goos/$goarch
Network config: config/unified-network.mainnet.json

Included:
- bin/unified-node
- bin/unified-cli
- deploy templates
- ops/install and backup scripts
- docs/runbook.md
EOF

  tar -C "$TMP_DIR" -czf "$artifact_path" "$bundle_name"
  append_checksum "$artifact_path"
  ARTIFACT_MANIFEST_LINES+=("{\"name\":\"$(basename "$artifact_path")\",\"kind\":\"runtime\",\"target\":\"${goos}/${goarch}\",\"sha256\":\"$(sha256_file "$artifact_path")\"}")
}

package_desktop_bundle() {
  local desktop_dir="$ROOT_DIR/web/desktop"
  local platform
  local bundle_name
  local stage_dir
  local artifact_path

  [ -d "$desktop_dir" ] || fail "desktop directory not found: $desktop_dir"
  require_cmd "$NPM_BIN"

  platform="$(host_platform)"
  bundle_name="unified-desktop-${RELEASE_VERSION}-${platform}"
  stage_dir="$TMP_DIR/$bundle_name"
  artifact_path="$ARTIFACTS_DIR/${bundle_name}.tar.gz"

  log "building desktop bundle for ${platform}"
  (
    cd "$desktop_dir"
    if [ ! -d node_modules ]; then
      "$NPM_BIN" ci
    fi
    "$NPM_BIN" run build
  )

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"
  cp -R "$desktop_dir/dist" "$stage_dir/dist"
  cp -R "$desktop_dir/electron" "$stage_dir/electron"
  cp "$desktop_dir/package.json" "$stage_dir/package.json"
  cp "$desktop_dir/package-lock.json" "$stage_dir/package-lock.json"
  cp "$desktop_dir/index.html" "$stage_dir/index.html"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"

  if [ "$RELEASE_INCLUDE_DESKTOP_NODE_MODULES" = "1" ] && [ -d "$desktop_dir/node_modules" ]; then
    cp -R "$desktop_dir/node_modules" "$stage_dir/node_modules"
  fi

  cat > "$stage_dir/run-unified-desktop.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ ! -d "$ROOT_DIR/node_modules" ]; then
  npm ci
fi
npm run start
EOF
  chmod +x "$stage_dir/run-unified-desktop.sh"

  cat > "$stage_dir/RELEASE.txt" <<EOF
UniFied Desktop Bundle
Version: $RELEASE_VERSION
Platform: $platform

Run:
  ./run-unified-desktop.sh

If node_modules is not included, the script will run npm ci first.
Set the RPC endpoint in the app to your node's /rpc URL or local SSH tunnel.
EOF

  tar -C "$TMP_DIR" -czf "$artifact_path" "$bundle_name"
  append_checksum "$artifact_path"
  ARTIFACT_MANIFEST_LINES+=("{\"name\":\"$(basename "$artifact_path")\",\"kind\":\"desktop\",\"target\":\"${platform}\",\"sha256\":\"$(sha256_file "$artifact_path")\"}")
}

package_website_bundle() {
  local bundle_name="unified-website-${RELEASE_VERSION}"
  local stage_dir="$TMP_DIR/$bundle_name"
  local artifact_path="$ARTIFACTS_DIR/${bundle_name}.tar.gz"

  log "packaging website bundle"
  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"
  mkdir -p "$stage_dir/web"
  cp -R "$ROOT_DIR/web/site" "$stage_dir/web/site"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"

  cat > "$stage_dir/RELEASE.txt" <<EOF
UniFied Website Bundle
Version: $RELEASE_VERSION

Included:
- web/site/ static assets
- dashboard.html for live RPC exploration

Deploy behind HTTPS and proxy /rpc and /ws to the node for same-origin browser access.
EOF

  tar -C "$TMP_DIR" -czf "$artifact_path" "$bundle_name"
  append_checksum "$artifact_path"
  ARTIFACT_MANIFEST_LINES+=("{\"name\":\"$(basename "$artifact_path")\",\"kind\":\"website\",\"target\":\"static\",\"sha256\":\"$(sha256_file "$artifact_path")\"}")
}

package_ops_bundle() {
  local bundle_name="unified-mainnet-ops-${RELEASE_VERSION}"
  local stage_dir="$TMP_DIR/$bundle_name"
  local artifact_path="$ARTIFACTS_DIR/${bundle_name}.tar.gz"

  log "packaging ops bundle"
  rm -rf "$stage_dir"
  mkdir -p "$stage_dir/config" "$stage_dir/contracts" "$stage_dir/docs"
  cp -R "$ROOT_DIR/deploy" "$stage_dir/deploy"
  cp -R "$ROOT_DIR/scripts" "$stage_dir/scripts"
  cp "$RELEASE_NETWORK_CONFIG" "$stage_dir/config/unified-network.mainnet.json"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"
  cp "$ROOT_DIR/docs/runbook.md" "$stage_dir/docs/runbook.md"

  if [ "$RELEASE_INCLUDE_CONTRACTS" = "1" ]; then
    cp -R "$ROOT_DIR/contracts/." "$stage_dir/contracts/"
    if [ -f "$ROOT_DIR/contracts/artifacts/UNS.bin" ]; then
      cp "$ROOT_DIR/contracts/artifacts/UNS.bin" "$stage_dir/contracts/"
    fi
    if [ -f "$ROOT_DIR/contracts/artifacts/IUFDToken.bin" ]; then
      cp "$ROOT_DIR/contracts/artifacts/IUFDToken.bin" "$stage_dir/contracts/"
    fi
    if command -v npx >/dev/null 2>&1; then
      (
        cd "$ROOT_DIR"
        $SOLC_BIN --bin --abi -o "$stage_dir/contracts" contracts/UNS.sol >/dev/null 2>&1 || true
      )
    fi
  fi

  cat > "$stage_dir/RELEASE.txt" <<EOF
UniFied Mainnet Ops Bundle
Version: $RELEASE_VERSION

Included:
- network config
- deploy templates
- ops scripts
- launch runbook
- UNS contract source and compiled artifacts when available
EOF

  tar -C "$TMP_DIR" -czf "$artifact_path" "$bundle_name"
  append_checksum "$artifact_path"
  ARTIFACT_MANIFEST_LINES+=("{\"name\":\"$(basename "$artifact_path")\",\"kind\":\"ops\",\"target\":\"all\",\"sha256\":\"$(sha256_file "$artifact_path")\"}")
}

write_manifest() {
  local config_hash artifact_lines joined
  config_hash="$(sha256_file "$RELEASE_NETWORK_CONFIG")"
  joined=""
  local line
  for line in "${ARTIFACT_MANIFEST_LINES[@]}"; do
    if [ -n "$joined" ]; then
      joined="${joined},"
    fi
    joined="${joined}${line}"
  done

  cat > "$MANIFEST_PATH" <<EOF
{
  "releaseVersion": "$RELEASE_VERSION",
  "builtAtUTC": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "rootDir": "$ROOT_DIR",
  "networkConfig": "$(basename "$RELEASE_NETWORK_CONFIG")",
  "networkConfigSha256": "$config_hash",
  "artifacts": [$joined]
}
EOF
}

write_summary() {
  cat > "$SUMMARY_PATH" <<EOF
UniFied Mainnet Release Package
Version: $RELEASE_VERSION
Built at: $(date -u +%Y-%m-%dT%H:%M:%SZ)
Network config: $RELEASE_NETWORK_CONFIG

Artifacts:
$(cd "$ARTIFACTS_DIR" && ls -1)

Checksums:
  $CHECKSUMS_PATH

Manifest:
  $MANIFEST_PATH
EOF
}

package_meta_archive() {
  local meta_name="unified-mainnet-launch-${RELEASE_VERSION}.tar.gz"
  local meta_path="$RELEASE_OUT/$meta_name"

  tar -C "$RELEASE_OUT" -czf "$meta_path" artifacts "$(basename "$MANIFEST_PATH")" "$(basename "$CHECKSUMS_PATH")" "$(basename "$SUMMARY_PATH")"
  append_checksum "$meta_path"
  log "wrote meta archive $(basename "$meta_path")"
}

main() {
  require_cmd "$GO_BIN"
  require_cmd tar
  require_cmd date
  mkdir -p "$ARTIFACTS_DIR"
  : > "$CHECKSUMS_PATH"

  [ -f "$RELEASE_NETWORK_CONFIG" ] || fail "network config not found: $RELEASE_NETWORK_CONFIG"

  TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/unified-release.XXXXXX")"
  trap 'rm -rf "$TMP_DIR"' EXIT

  ARTIFACT_MANIFEST_LINES=()

  local target
  for target in $RELEASE_TARGETS; do
    package_runtime_bundle "$target"
  done

  package_ops_bundle

  if [ "$RELEASE_INCLUDE_WEBSITE" = "1" ]; then
    package_website_bundle
  fi

  if [ "$RELEASE_INCLUDE_DESKTOP" = "1" ]; then
    package_desktop_bundle
  fi

  write_manifest
  write_summary
  package_meta_archive

  log "release package ready at $RELEASE_OUT"
}

main "$@"
