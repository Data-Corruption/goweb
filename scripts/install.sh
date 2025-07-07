#!/usr/bin/env bash

# Generic install script, assumes user has base linux system, no deps for install.
# Very simple and durable, for single bin apps targeting linux x86_64/amd64.
# Might add non sudo support later, but this works for now.
#
# Default (installs latest version to /usr/local/bin):
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | sudo bash
#
# With version and install dir override:
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | sudo bash -s -- [VERSION] [INSTALL_DIR]
#
# Arguments:
#   [VERSION]      Optional tag (e.g. v1.2.3). Default = latest
#   [INSTALL_DIR]  Optional install dir.       Default = /usr/local/bin

# Template variables ----------------------------------------------------------

# Replace with your GitHub repository details and app name
OWNER="some-user"
REPO="some-repo"
APP_NAME="goweb"

# startup ---------------------------------------------------------------------

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
    mv "$old_bin" "$install_path" # if this or anything in rollback fails, no err handling, this is the err handling lmao
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

DEFAULT_INSTALL_DIR="/usr/local/bin"
VERSION="${1:-latest}"
INSTALL_DIR="${2:-$DEFAULT_INSTALL_DIR}"
BIN_ASSET_NAME="linux-amd64.gz"

# ensure sudo
if [[ $EUID -ne 0 ]]; then
   echo "🔴 This script must be run as root (use sudo)" >&2
   exit 1
fi

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

# check if  INSTALL_DIR is on the user’s PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo "🟡  $INSTALL_DIR isn't on your \$PATH."
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
if [[ "$INSTALL_DIR" != "$DEFAULT_INSTALL_DIR" ]]; then
  echo "📦 Installing $APP_NAME $VERSION to custom directory: $INSTALL_DIR ..."
else
  echo "📦 Installing $APP_NAME $VERSION ..."
fi

# download the binary ---------------------------------------------------------

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

# download the binary
echo "Downloading $bin_url"
curl --max-time 300 --retry 2 --retry-delay 2 --fail --show-error --location --progress-bar -o "$dwld_out" "$bin_url"

# extract the downloaded archive
echo "Unzipping..."
if ! gzip -d "$dwld_out"; then
  echo "🔴 Failed to extract binary." >&2
  exit 1
fi

# backup existing install in case of failure
old_bin=""
if [[ -f "$install_path" ]]; then
  old_bin="$temp_dir/$APP_NAME.old"
  mv "$install_path" "$old_bin"
fi

# install the binary
echo "Installing binary ..."
if ! install -Dm755 "$gzip_out" "$install_path"; then
  echo "🔴 Failed to install new binary." >&2
  exit 1
fi

# helper to detect if running in WSL
is_wsl() {
  [[ -n ${WSL_DISTRO_NAME-} || -n ${WSL_INTEROP-} ]] && return 0
  grep -qiE '(microsoft|wsl)' /proc/version 2>/dev/null # fallback for older WSL versions
}

# try to set privileged port capabilities
if ! is_wsl; then # WSL doesn't support setcap
  if command -v setcap >/dev/null 2>&1; then
    setcap 'cap_net_bind_service=+ep' "$install_path" || {
      echo "🟡 Warning: Failed to set capabilities on $install_path"
      echo "   If needed for privileged ports (e.g. 80/443) aka http/https, run:"
      echo "     sudo setcap 'cap_net_bind_service=+ep' $install_path"
    }
  fi
fi

# test the bin
if ! "$install_path" -v >/dev/null 2>&1; then
  echo "🔴 Failed to verify installation of $install_path" >&2
  exit 1
fi

echo "🟢 Successfully installed $APP_NAME $VERSION. Run $APP_NAME -v to verify."