#!/usr/bin/env bash
set -euo pipefail # exit on error
trap 'rm -f "${response:-}" "${headers:-}" "${download_temp:-}" "${extracted_temp:-}" 2>/dev/null || true' EXIT

# generic install script, assumes user has nothing installed yet, just base linux system
#
# One‑liner (latest release):
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | sudo bash
#
# Custom version or install directory:
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | VERSION=v1.2.3 INSTALL_DIR=/opt/bin sudo bash
#
# Positional args
#   [VERSION]      Optional tag (e.g. v1.2.3). Default = latest
#   [INSTALL_DIR]  Optional install dir. Default = /usr/local/bin
#
# sudo needed for copying bin to /usr/local/bin and setcap to allow binding to privileged ports (e.g. 80, 443)

OWNER="someuser"
REPO="somerepo"

# startup ---------------------------------------------------------------------

# parse args
VERSION="${1:-${VERSION:-latest}}"
INSTALL_DIR="${2:-${INSTALL_DIR:-/usr/local/bin}}"

# ensure sudo
if [[ $EUID -ne 0 ]]; then
   echo "🔴 This script must be run as root (use sudo)"
   exit 1
fi

# get os and arch -------------------------------------------------------------

# detect platform
uname_s=$(uname -s) # OS
uname_m=$(uname -m) # Architecture

# if not linux, exit
if [[ "$uname_s" != "Linux" ]]; then
  echo "🔴 This script is only supported on Linux. Detected OS: $uname_s"
  exit 1
fi
goos="linux"

# map Architecture → GOARCH
case "$uname_m" in
  x86_64|amd64)            goarch="amd64"  ;;
  i386|i686)               goarch="386"    ;;
  armv6l|armv7l|armv8l)    goarch="arm"    ;;
  aarch64|arm64)           goarch="arm64"  ;;
  ppc64)                   goarch="ppc64"  ;;
  ppc64le)                 goarch="ppc64le";;
  s390x)                   goarch="s390x"  ;;
  riscv64)                 goarch="riscv64";;
  mips)                    goarch="mips"   ;;
  mipsle)                  goarch="mipsle" ;;
  mips64)                  goarch="mips64" ;;
  mips64le)                goarch="mips64le" ;;
  sparc64)                 goarch="sparc64";;
  loongarch64)             goarch="loong64";;
  *)
    echo "🔴 Unsupported/unknown architecture: $uname_m" >&2; exit 1 ;;
esac

# download the binary ---------------------------------------------------------

ARCHIVE_EXT=".gz"
MATCH_SUFFIX="${goos}-${goarch}${ARCHIVE_EXT}"

# Determine API URL based on version
if [[ "$VERSION" == "latest" ]]; then
  API_URL="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"
else
  API_URL="https://api.github.com/repos/${OWNER}/${REPO}/releases/tags/${VERSION}"
fi

# maybe use this in the future somehow, leaving it here for now
AUTH_HEADER=""
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
  AUTH_HEADER="Authorization: token $GITHUB_TOKEN"
fi

# fetch headers and body
echo "📦 Fetching release data for $OWNER/$REPO (version: $VERSION)..."
response=$(mktemp)
headers=$(mktemp)

if [[ -n "$AUTH_HEADER" ]]; then
  curl -sSL -D "$headers" -H "$AUTH_HEADER" "$API_URL" -o "$response"
else
  curl -sSL -D "$headers" "$API_URL" -o "$response"
fi

# check status
http_status=$(grep -E '^HTTP/[0-9.]+ [0-9]+' "$headers" | head -1 | awk '{print $2}')
if [[ "$http_status" != "200" ]]; then
  echo "🔴 Failed to fetch release data. HTTP status: $http_status"
  cat "$response" >&2
  exit 1
fi

# rate limit check
remaining=$(awk -F': ' '/^[Xx]-[Rr]ate[Ll]imit-[Rr]emaining:/ {print $2}' "$headers" | tr -d '\r')
reset_epoch=$(awk -F': ' '/^[Xx]-[Rr]ate[Ll]imit-[Rr]eset:/ {print $2}' "$headers" | tr -d '\r')

if [[ "${remaining:-1}" -le 0 ]]; then
  if command -v date >/dev/null 2>&1; then
    reset_time=$(date -d "@$reset_epoch" +"%Y-%m-%d %H:%M:%S" 2>/dev/null || date -r "$reset_epoch" +"%Y-%m-%d %H:%M:%S" 2>/dev/null || echo "timestamp: $reset_epoch")
  else
    reset_time="timestamp: $reset_epoch"
  fi
  echo "🔴 GitHub API rate limit exceeded. Limit should reset at $reset_time UTC."
  exit 1
fi

# extract all asset URLs and names
assets=$(cat "$response" | tr -d '\n' | sed 's/}/}\n/g' | grep '"browser_download_url"' | sed 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1|\2/g')

# find matching asset
download_url=""
asset_name=""
while IFS='|' read -r name url; do
  if [[ "$name" == *"-${MATCH_SUFFIX}" ]]; then
    download_url="$url"
    asset_name="$name"
    break
  fi
done <<< "$assets"

if [[ -z "$download_url" ]]; then
  echo "🔴 No asset found matching pattern: *-${MATCH_SUFFIX}"
  echo "   Expected format: example-${goos}-${goarch}.gz"
  exit 1
fi

# extract app name from asset name (everything before the first dash)
app_name=$(echo "$asset_name" | sed 's/-.*$//')

# set target path
target_path="$INSTALL_DIR/$app_name"

# check if already installed 
if [[ -f "$target_path" ]]; then
    echo "⚠️  $app_name is already installed at $target_path"
    read -p "   Overwrite? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Installation cancelled."
        exit 0
    fi
fi

echo "📥 Downloading: $download_url"

# download the asset
download_temp=$(mktemp)
if [[ -n "$AUTH_HEADER" ]]; then
  curl -sSL -H "$AUTH_HEADER" "$download_url" -o "$download_temp"
else
  curl -sSL "$download_url" -o "$download_temp"
fi

# extract from .gz
echo "📦 Extracting $app_name..."
extracted_temp=$(mktemp)
gunzip -c "$download_temp" > "$extracted_temp"

# Create install directory if it doesn't exist
if [[ ! -d "$INSTALL_DIR" ]]; then
  echo "Creating install directory: $INSTALL_DIR"
  mkdir -p "$INSTALL_DIR" || {
    echo "🔴 Failed to create install directory."
    exit 1
  }
fi

# install the binary ----------------------------------------------------------

echo "Installing to: $target_path"

# try to move/copy the file
if mv "$extracted_temp" "$target_path" 2>/dev/null || cp "$extracted_temp" "$target_path" 2>/dev/null; then
  chmod +x "$target_path"
  setcap 'cap_net_bind_service=+ep' "$target_path"
else
  echo "🔴 Failed to install."
  exit 1
fi

if ! echo "$PATH" | grep -qF "$INSTALL_DIR"; then
  echo "⚠️  Note: $INSTALL_DIR is not in your PATH"
  echo "   Add it with: export PATH=\"$INSTALL_DIR:\$PATH\" or add it to your shell config file (e.g., ~/.bashrc, ~/.zshrc)"
fi

echo "🟢 Successfully installed! Run '$app_name -v' to verify."
echo "   You may need to restart your terminal session first."