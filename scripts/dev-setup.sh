#!/usr/bin/env bash

# Template variables ----------------------------------------------------------

APP_NAME="goweb"

# Startup ---------------------------------------------------------------------

set -euo pipefail
umask 022

DATA_PATH="/var/lib/$APP_NAME"

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

# if not root, exit
if [[ $EUID -ne 0 ]]; then
  echo "🔴 This script must be run as root. Please run with sudo." >&2
  exit 1
fi

# dep check for bare bone distros
required_bins=(install)
for bin in "${required_bins[@]}"; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "🔴 Missing required tool: $bin. Please install it and re-run." >&2
    exit 1
  fi
done

# system group
getent group "$APP_NAME" >/dev/null || groupadd --system "$APP_NAME"

# system user for daemon
if ! id -u "$APP_NAME" >/dev/null 2>&1; then
  NOLOGIN="$(command -v nologin || true)"
  useradd --system --home "$DATA_PATH" --shell "${NOLOGIN:-/usr/sbin/nologin}" --gid "$APP_NAME" "$APP_NAME"
fi

# data / env config dir
install -d -o root -g "$APP_NAME" -m 02770 "$DATA_PATH"
install -d -o root -g "$APP_NAME" -m 02770 "/etc/$APP_NAME"

# add invoking (non-root) user to the group so they can use the CLI without sudo
added_user=0
invoker="${SUDO_USER:-}"
if [[ -n "$invoker" && "$invoker" != "root" ]]; then
  if ! id -nG "$invoker" 2>/dev/null | tr ' ' '\n' | grep -qx "$APP_NAME"; then
    usermod -aG "$APP_NAME" "$invoker"
    added_user=1
  fi
fi

if (( added_user )); then
  echo "Added $invoker to group $APP_NAME."
  echo "➡ Re-login (or run: newgrp $APP_NAME) before using the CLI without sudo."
elif [[ -z "${SUDO_USER:-}" || "${SUDO_USER:-}" == "root" ]]; then
  echo "Tip: allow users with: sudo usermod -aG $APP_NAME <user>"
fi
