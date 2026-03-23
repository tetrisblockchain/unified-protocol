#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25
GO_VERSION="1.25.0"
GO_BINARY_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"

# --- UI Helpers ---

# Simple progress bar: progress_bar <duration_seconds> <label>
progress_bar() {
    local duration=$1
    local label=$2
    local width=40
    echo -n "$label "
    for ((i=0; i<=width; i++)); do
        local percent=$(( i * 100 / width ))
        local filled=$(( i ))
        local empty=$(( width - i ))
        printf "\r%s [%3d%%] [%.s#%.0s]" "$label" "$percent" $(seq 1 $filled) $(seq 1 $empty)
        sleep "$(bc -l <<< "$duration/$width")"
    done
    echo ""
}

show_spinner() {
    local pid=$1
    local delay=0.1
    local spinstr='|/-\'
    while [ "$(ps a | awk '{print $1}' | grep $pid)" ]; do
        local temp=${spinstr#?}
        printf " [%c]  " "$spinstr"
        local spinstr=$temp${spinstr%"$temp"}
        sleep $delay
        printf "\b\b\b\b\b\b"
    done
    printf "    \b\b\b\b"
}

# --- Logic ---

have_command() {
    command -v "$1" >/dev/null 2>&1
}

parse_go_version() {
    local raw
    raw="$(go env GOVERSION 2>/dev/null || true)"
    if [[ -z "$raw" ]]; then
        raw="$(go version 2>/dev/null | awk '{print $3}')" || true
    fi
    raw="${raw#go}"
    echo "$raw"
}

check_go_version() {
    local version="$(parse_go_version)"
    if [[ -z "$version" ]]; then return 1; fi
    
    local major="${version%%.*}"
    local minor="${version#*.}"
    minor="${minor%%.*}"

    if (( major < MIN_GO_MAJOR )); then return 1; fi
    if (( major == MIN_GO_MAJOR && minor < MIN_GO_MINOR )); then return 1; fi
    return 0
}

install_go_linux() {
    echo "Starting Installation for Go $GO_VERSION..."
    
    echo -n "Updating system packages..."
    sudo apt update > /dev/null 2>&1 & show_spinner $!
    echo " Done."

    echo -n "Removing old Go versions..."
    sudo rm -rf /usr/local/go
    sudo apt remove --purge golang-go golang-1.* -y > /dev/null 2>&1 & show_spinner $!
    echo " Cleaned."

    local tmp_file="/tmp/go_dist.tar.gz"
    echo "Downloading Go Binary..."
    # Real progress bar using wget's output
    wget --progress=dot "$GO_BINARY_URL" -O "$tmp_file" 2>&1 | grep --line-buffered "%" | \
        sed -u -e "s/\([0-9]\+\)%/\1/" | while read -r n; do
            local w=$(( n / 4 ))
            printf "\rProgress: [%-25s] %d%%" "$(printf '#%.0s' $(seq 1 $w))" "$n"
        done
    echo ""

    echo -n "Extracting to /usr/local..."
    sudo tar -C /usr/local -xzf "$tmp_file" & show_spinner $!
    echo " Extracted."

    if ! grep -q "/usr/local/go/bin" "$HOME/.profile"; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> "$HOME/.profile"
    fi
    
    rm "$tmp_file"
    export PATH=$PATH:/usr/local/go/bin
    echo "Go $GO_VERSION is now installed."
}

# --- Main Script ---

clear
echo "===================================================="
echo "      UniFied Protocol Environment Setup            "
echo "===================================================="
echo "Target Workspace: $ROOT_DIR"

if ! have_command go || ! check_go_version; then
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        echo "⚠️  Go $MIN_GO_MAJOR.$MIN_GO_MINOR+ is missing or outdated."
        read -p "Download and install Go automatically? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            install_go_linux
        else
            exit 1
        fi
    else
        echo "❌ Error: Go $MIN_GO_MAJOR.$MIN_GO_MINOR+ required."
        exit 1
    fi
fi

progress_bar 1 "Initializing Workspace..."
mkdir -p "$ROOT_DIR/build" "$ROOT_DIR/data/local" "$ROOT_DIR/logs"

echo "Downloading Go module dependencies..."
(cd "$ROOT_DIR" && go mod download) & show_spinner $!
echo " Dependencies synced."

echo "----------------------------------------------------"
echo "✅ Setup Complete!"
echo "----------------------------------------------------"
cat <<EOF
Next Steps:
  1. Run 'source ~/.profile' to update your current path.
  2. Run 'make build' to compile the protocol.

See docs/runbook.md for more details.
EOF
