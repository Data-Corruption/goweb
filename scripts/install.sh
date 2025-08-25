#!/usr/bin/env bash

# Generic install / update script, for self-contained apps targeting linux x86_64/amd64.
# Shared app data, regardless of user, limited to those added to the app group.
# App's daemon, if present, is just a sub-command.
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | sudo bash -s -- [VERSION]
#
# Arguments:
#   [VERSION] Optional tag (e.g. v1.2.3). Default = latest

# Template variables ----------------------------------------------------------

REPO_OWNER="Data-Corruption"
REPO_NAME="goweb"
APP_NAME="goweb"

SERVICE="true"
SERVICE_DESC="web server daemon for CLI application goweb"
SERVICE_ARGS="serve"

# Startup ---------------------------------------------------------------------

set -euo pipefail
umask 022

INSTALL_PATH="/usr/local/bin/$APP_NAME"
DATA_PATH="/var/lib/$APP_NAME"

SERVICE_NAME="$APP_NAME.service"
SERVICE_PATH="/etc/systemd/system/$SERVICE_NAME"
ACTIVE_TIMEOUT=10 # seconds to wait until service to become active
HEALTH_TIMEOUT=30 # seconds to wait until service writes healthy file signal

VERSION="${1:-latest}"
BIN_ASSET_NAME="linux-amd64.gz"

temp_dir=""
cleanup() {
  if [[ -d "$temp_dir" ]]; then
    rm -rf "$temp_dir"
  fi
}

old_bin=""
was_enabled=0
was_active=0
unit_known=0
rollback() {
  if [[ "$SERVICE" == "true" && "$unit_known" == "1" ]]; then
    systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    systemctl reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi

  if [[ -f "$old_bin" ]]; then
    echo "Restoring previous binary from $old_bin ..."
    mv -f "$old_bin" "$INSTALL_PATH" || echo "   Warning: Failed to restore old binary"
  fi

  if [[ "$SERVICE" == "true" && "$unit_known" == "1" ]]; then
    systemctl daemon-reload >/dev/null 2>&1 || true

    if [[ "$was_enabled" == "1" ]]; then
      systemctl enable "$SERVICE_NAME"  >/dev/null 2>&1 || true
    else
      systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
    fi

    if [[ "$was_active" == "1" ]]; then
      systemctl start "$SERVICE_NAME"   >/dev/null 2>&1 || true
    fi
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
required_bins=(curl gzip install awk)
for bin in "${required_bins[@]}"; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "🔴 Missing required tool: $bin. Please install it and re-run." >&2
    exit 1
  fi
done

if [[ "$SERVICE" == "true" ]]; then
  # require systemd ≥ 245
  systemdVersion=$(systemctl --version | head -n1 | awk '{print $2}')
  if (( systemdVersion < 245 )); then
    echo "Error: systemd ≥ 245 required, found $systemdVersion" >&2
    exit 1
  fi
  # track prior systemd state (for rollback)
  if systemctl cat "$SERVICE_NAME" >/dev/null 2>&1 || [[ -f "$SERVICE_PATH" ]]; then
    unit_known=1
    systemctl is-enabled --quiet "$SERVICE_NAME" && was_enabled=1 || true
    systemctl is-active  --quiet "$SERVICE_NAME" && was_active=1  || true
  fi
fi

# looks good, print info
echo "📦 Installing $APP_NAME $VERSION ..."

# User / Dirs -----------------------------------------------------------------

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

# Download the binary ---------------------------------------------------------

bin_url=""
if [[ "$VERSION" == "latest" ]]; then
  bin_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/latest/download/${BIN_ASSET_NAME}"
else
  bin_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${VERSION}/${BIN_ASSET_NAME}"
fi

temp_dir=$(mktemp -d)
dwld_out="${temp_dir}/${BIN_ASSET_NAME}"
gzip_out="${dwld_out%.gz}"

# curl time! curl moment!
echo "Downloading binary from $bin_url"
curl --max-time 300 --retry 3 --retry-all-errors --retry-delay 1 --fail --show-error --location --progress-bar -o "$dwld_out" "$bin_url"

echo "Unzipping..."
gzip -dc "$dwld_out" > "$gzip_out" || { echo "🔴 Failed to unzip"; exit 1; }

# backup existing install in case of failure
if [[ -f "$INSTALL_PATH" ]]; then
  old_bin="$temp_dir/$APP_NAME.old"
  echo "Backing up current binary to $old_bin (will restore on failure) ..."
  mv -f "$INSTALL_PATH" "$old_bin"
fi

# install the binary
echo "Installing binary ..."
install -Dm755 "$gzip_out" "$INSTALL_PATH" || { echo "🔴 Failed to install binary."; exit 1; }

# verify install / get version
EFFECTIVE_VER="$("$INSTALL_PATH" -v 2>/dev/null | head -n1)" || { echo "🔴 Failed to verify installation of $INSTALL_PATH" >&2; exit 1; }

# Service ---------------------------------------------------------------------

if [[ "$SERVICE" == "true" ]]; then
  # escape percent in service args
  SAFE_ARGS="${SERVICE_ARGS//%/%%}"

  # write unit file, (overwrite is ok, this file is not advertized to users, they shouldn't have edited it)
  cat >"$SERVICE_PATH" <<EOF
[Unit]
Description=${SERVICE_DESC}
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=600
StartLimitBurst=5

[Service]
User=${APP_NAME}
Group=${APP_NAME}
WorkingDirectory=${DATA_PATH}
ExecStart=${INSTALL_PATH} ${SAFE_ARGS}
Restart=always
RestartSec=5

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=${DATA_PATH} /etc/${APP_NAME}
LockPersonality=yes
MemoryDenyWriteExecute=yes
LimitNOFILE=65535
UMask=0007
RestrictSUIDSGID=yes
ProtectClock=yes
ProtectHostname=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

EnvironmentFile=-/etc/${APP_NAME}/${APP_NAME}.env

[Install]
WantedBy=multi-user.target
EOF

  # delete health file
  rm -f "$DATA_PATH/health"

  # enable and start/restart service
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"

  # helper for getting restart count / parsing to integer
  get_restarts() {
    local unit="$1"
    local v
    v="$(systemctl show -p NRestarts --value -- "$unit" 2>/dev/null || true)"
    # trim whitespace/newlines just in case
    v="${v//$'\n'/}"
    v="${v//[$' \t\r']/}"
    # coerce to 0 if empty or non-numeric
    [[ "$v" =~ ^[0-9]+$ ]] || v=0
    printf '%s' "$v"
  }

  n_res_before="$(get_restarts "$SERVICE_NAME")"

  # start/restart
  if systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "Restarting service..."
    systemctl restart "$SERVICE_NAME"
  else
    echo "Starting service..."
    systemctl start "$SERVICE_NAME"
  fi

  # health gate

  # wait for service to become active
  deadline=$(( SECONDS + ${ACTIVE_TIMEOUT} ))
  until systemctl is-active --quiet "$SERVICE_NAME"; do
    if (( SECONDS >= deadline )); then
      echo "🔴 Service failed to reach active state within timeout." >&2
      exit 1
    fi
    sleep 1
  done

  # wait for health file creation or HEALTH_TIMEOUT
  deadline=$(( SECONDS + ${HEALTH_TIMEOUT} ))
  until [[ -f "$DATA_PATH/health" ]]; do
    if (( SECONDS >= deadline )); then
      echo "🔴 Service failed to create health file within timeout." >&2
      exit 1
    fi
    sleep 1
  done

  # final check (is active + no unexpected restarts)
  if ! systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "🔴 Service failed to reach healthy active state." >&2
    exit 1
  fi
  n_res_after="$(get_restarts "$SERVICE_NAME")"
  if (( n_res_after > n_res_before )); then
    echo "🔴 Unexpected restart(s) detected." >&2
    exit 1
  fi

  echo ""
  echo "🖧 Service: $SERVICE_NAME"
  echo "    Status:  sudo systemctl status $SERVICE_NAME"
  echo "    Start:   sudo systemctl start $SERVICE_NAME"
  echo "    Restart: sudo systemctl restart $SERVICE_NAME"
  echo "    Reset:   sudo systemctl reset-failed $SERVICE_NAME"
  echo "    Env:     sudo \${EDITOR:-nano} /etc/$APP_NAME/$APP_NAME.env && sudo systemctl restart $SERVICE_NAME"
fi

echo ""
echo "🟢 Installed: $APP_NAME (${EFFECTIVE_VER:-$VERSION}) → $INSTALL_PATH"
echo ""

if (( added_user )); then
  echo "Added $invoker to group $APP_NAME."
  echo "➡ Re-login (or run: newgrp $APP_NAME) before using the CLI without sudo."
elif [[ -z "${SUDO_USER:-}" || "${SUDO_USER:-}" == "root" ]]; then
  echo "Tip: allow users with: sudo usermod -aG $APP_NAME <user>"
fi
