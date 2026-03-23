#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25
GO_VERSION="1.25.0"
GO_BINARY_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"

# --- UI Helpers ---

show_spinner() {
    local pid=$1
    local delay=0.1
    local spinstr='|/-\'
    while kill -0 "$pid" 2>/dev/null; do
        local temp=${spinstr#?}
        printf " [%c]  " "$spinstr"
        spinstr=$temp${spinstr%"$temp"}
        sleep $delay
        printf "\b\b\b\b\b\b"
    done
    printf "    \b\b\b\b"
}

# Fixed Download Progress Bar
download_go() {
    local url=$1
    local dest=$2
    echo "Downloading Go Binary ($GO_VERSION)..."
    # Using curl for more reliable progress parsing
    curl -L "$url" -o "$dest" --progress-bar 2>&1 | \
    while read -r -d $'\r' line; do
        # Extract percentage, ignoring non-numeric characters
        if [[ "$line" =~ ([0-9]+(\.[0-9]+)?)% ]]; then
            local percent="${BASH_REMATCH[1]%.*}"
            local filled=$(( percent / 4 ))
            local empty=$(( 25 - filled ))
            printf "\rProgress: [%-25s] %d%%" "$(printf '#%.0s' $(seq 1 $filled 2>/dev/null || echo ""))" "$percent"
        fi
    done
    echo -e "\nDownload Complete."
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
    local version
    version="$(parse_go_version)"
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
    download_go "$GO_BINARY_URL" "$tmp_file"

    echo -n "Extracting to /usr/local..."
    sudo tar -C /usr/local -xzf "$tmp_file" & show_spinner $!
    echo " Extracted."

    if ! grep -q "/usr/local/go/bin" "$HOME/.profile"; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> "$HOME/.profile"
    fi
    
    rm -f "$tmp_file"
    export PATH=$PATH:/usr/local/go/bin
}

# --- Main ---

clear
echo "===================================================="
echo "      UniFied Protocol Environment Setup            "
echo "===================================================="

if ! have_command go || ! check_go_version; then
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        echo "⚠️  Go $MIN_GO_MAJOR.$MIN_GO_MINOR+ is missing or outdated."
        read -p "Install Go $GO_VERSION automatically? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            install_go_linux
        else
            echo "Exit: Go manual install required."
            exit 1
        fi
    fi
fi

echo -n "Initializing workspace directories..."
mkdir -p "$ROOT_DIR/build" "$ROOT_DIR/data/local" "$ROOT_DIR/logs"
echo " Done."

echo -n "Downloading Go dependencies..."
(cd "$ROOT_DIR" && go mod download) & show_spinner $!
echo " Done."

echo "----------------------------------------------------"
echo "✅ Setup Complete!"
echo "----------------------------------------------------"
echo "Run: source ~/.profile"
echo "Then: make build"
