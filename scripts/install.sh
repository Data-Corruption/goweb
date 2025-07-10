#!/usr/bin/env bash

# Generic install script, assumes user has base linux system, no deps for install.
# Very simple and durable, for single bin apps targeting linux x86_64/amd64.
#
# Default (installs latest version to /usr/local/bin):
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | bash
#
# With version and install dir override:
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | bash -s -- [VERSION] [INSTALL_DIR]
#
# Arguments:
#   [VERSION]      Optional tag (e.g. v1.2.3). Default = latest
#   [INSTALL_DIR]  Optional install dir.       Default = root: /usr/local/bin, user: $HOME/.local/bin
#
# If app will need to bind to privileged ports (e.g. 80/443), non-wsl users may
# need to run `sudo setcap 'cap_net_bind_service=+ep' install_path` after
# running. The script will print the install_path so users can easily copy it.

# Template variables ----------------------------------------------------------

# Replace with your GitHub repository details and app name
OWNER="Data-Corruption"
REPO="goweb"
APP_NAME="goweb"

# Startup ---------------------------------------------------------------------

set -euo pipefail
umask 022

temp_dir=""
cleanup() {
  if [[ -d "$temp_dir" ]]; then
    rm -rf "$temp_dir"
  fi
}

install_path=""
old_bin=""
rollback() {
  if [[ -f "$old_bin" ]]; then
    echo "   Restoring old installation..."
    mv "$old_bin" "$install_path" || echo "   Warning: Failed to restore old binary"
  fi
}

trap '
  status=$?
  if [[ $status -ne 0 ]]; then
    rollback
  fi
  cleanup
  exit $status
' EXIT

# default install directory. root: /usr/local/bin, user: $HOME/.local/bin
default_install_dir="/usr/local/bin"
if [[ $EUID -ne 0 ]]; then
  default_install_dir="$HOME/.local/bin"
  mkdir -p "$default_install_dir"
fi

VERSION="${1:-latest}"
INSTALL_DIR="${2:-$default_install_dir}"
BIN_ASSET_NAME="linux-amd64.gz"

# detect platform
uname_s=$(uname -s) # OS
uname_m=$(uname -m) # Architecture

# if not linux, exit
if [[ "$uname_s" != "Linux" ]]; then
  echo "🔴 This application is only supported on Linux. Detected OS: $uname_s" >&2
  exit 1
fi

# if not x86_64 or amd64 (some distros return this), exit
if [[ "$uname_m" != "x86_64" && "$uname_m" != "amd64" ]]; then
  echo "🔴 This application is only supported on x86_64/amd64. Detected architecture: $uname_m" >&2
  exit 1
fi

# check if install dir exists
if [[ ! -d "$INSTALL_DIR" ]]; then
  echo "🔴 Install directory does not exist: $INSTALL_DIR
  Please create it or specify a different directory." >&2
  exit 1
fi

# dep check for bare bone distros
required_bins=(curl gzip install) # setcap not in list cause it's not a thing on all distros and WSL
for bin in "${required_bins[@]}"; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "error: '$bin' is required but not installed or not in \$PATH" >&2
    exit 1
  fi
done

# looks good, print info
echo "📦 Installing $APP_NAME $VERSION to $INSTALL_DIR ..."

# check if  INSTALL_DIR is on the user’s PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo "🟡 WARNING! $INSTALL_DIR may not be on your \$PATH."
fi

# Download the binary ---------------------------------------------------------

bin_url=""
if [[ "$VERSION" == "latest" ]]; then
  bin_url="https://github.com/${OWNER}/${REPO}/releases/latest/download/${BIN_ASSET_NAME}"
else
  bin_url="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${BIN_ASSET_NAME}"
fi

install_path="$INSTALL_DIR/$APP_NAME"
temp_dir=$(mktemp -d)
dwld_out="${temp_dir}/${BIN_ASSET_NAME}"
gzip_out="${dwld_out%.gz}"

# curl time! curl moment!
echo "Downloading $bin_url"
curl --max-time 300 --retry 2 --retry-all-errors --retry-delay 2 --fail --show-error --location --progress-bar -o "$dwld_out" "$bin_url"

# unzip
echo "Unzipping..."
if ! gzip -d "$dwld_out"; then
  echo "🔴 Failed to unzip binary." >&2
  exit 1
fi

# backup existing install in case of failure
old_bin=""
if [[ -f "$install_path" ]]; then
  echo "Backing up existing install in case of failure ..."
  old_bin="$temp_dir/$APP_NAME.old"
  mv "$install_path" "$old_bin"
fi

# install the binary
echo "Installing binary ..."
if ! install -Dm755 "$gzip_out" "$install_path"; then
  echo "🔴 Failed to install new binary." >&2
  exit 1
fi

# test the bin
if ! "$install_path" -v >/dev/null 2>&1; then
  echo "🔴 Failed to verify installation of $install_path" >&2
  exit 1
fi

echo "🟢 Successfully installed $APP_NAME $VERSION to $install_path !"
echo "   Run $APP_NAME -v to verify. You may need to restart your terminal."