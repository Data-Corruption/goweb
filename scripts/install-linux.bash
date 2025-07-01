#!/bin/bash



INSTALL_DIR="/usr/local/bin" # should already be in the system PATH



INSTALL_DIR="/usr/local/bin" # This dir should already be in the system PATH
BINARY_PATH="unknown"
# EXECUTABLE_NAME is dynamically set by the build script, if you wish to change it, do so there
EXECUTABLE_NAME=""

declare -a binaries=("$EXECUTABLE_NAME-linux-amd64" "$EXECUTABLE_NAME-linux-arm64" "$EXECUTABLE_NAME-linux-riscv64")
declare -a foundBinaries

# Check each bin path to see if it exists in the current directory
for path in "${binaries[@]}"; do
  if [[ -e "$path" ]]; then
    foundBinaries+=("$path")
  fi
done

# Check how many valid bin paths were found
case ${#foundBinaries[@]} in
  0)  # No binaries were found
    echo "Error: No binaries were found in the current directory."
    exit 1
    ;;
  1)  # Exactly one binary was found
    BINARY_PATH=${foundBinaries[0]}
    ;;
  *)  # Multiple binaries found
    echo "Multiple binaries were found. Please choose one:"
    # List found binaries and prompt the user to choose
    select chosenPath in "${foundBinaries[@]}"; do
      if [[ " ${foundBinaries[*]} " =~ " ${chosenPath} " ]]; then
        echo "You selected: $chosenPath"
        BINARY_PATH=$chosenPath
        break
      else
        echo "Invalid selection. Please try again."
      fi
    done
    ;;
esac

# Check if the binary path is still unknown
if [[ "$BINARY_PATH" == "unknown" ]]; then
  echo "Error: Unable to determine which binary to use."
  exit 1
fi

# Make sure the install directory exists
if [[ ! -d "$INSTALL_DIR" ]]; then
  echo "Error: Install directory does not exist. `$INSTALL_DIR`"
  exit 1
fi

# Copy the binary to the install directory (overwrite if it exists)
cp "$BINARY_PATH" "$INSTALL_DIR/$EXECUTABLE_NAME"
# Check if the copy was successful
if [ $? -ne 0 ]; then
  echo "Failed to copy the binary to the install directory. Please check your permissions."
  exit 1
fi

# Make the executable... executable
chmod +x "$INSTALL_DIR/$EXECUTABLE_NAME"

# Allow the exe to use privileged ports
setcap 'cap_net_bind_service=+ep' "$INSTALL_DIR/$EXECUTABLE_NAME"

echo "Successfully installed $EXECUTABLE_NAME to $INSTALL_DIR/$EXECUTABLE_NAME"
echo "Please restart your terminal session to use $EXECUTABLE_NAME."
















#!/usr/bin/env bash
set -euo pipefail

OWNER="someuser"
REPO="somerepo"

INSTALL_DIR="/usr/local/bin" # should already be in the system PATH
ARCHIVE_EXT=".gz"
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)
MATCH_SUFFIX="${GOOS}-${GOARCH}${ARCHIVE_EXT}"

API_URL="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"

# Optional: provide a GitHub token to raise rate limit from 60 → 5000
AUTH_HEADER=""
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
  AUTH_HEADER="Authorization: token $GITHUB_TOKEN"
fi

# --- Fetch headers and body in one shot ---
echo "Fetching latest release data for $OWNER/$REPO..."
response=$(mktemp)
headers=$(mktemp)

curl -sSL -D "$headers" -H "$AUTH_HEADER" "$API_URL" -o "$response"

# --- Rate limit check ---
remaining=$(awk -F': ' '/^X-RateLimit-Remaining:/ {print $2}' "$headers" | tr -d '\r')
reset_epoch=$(awk -F': ' '/^X-RateLimit-Reset:/ {print $2}' "$headers" | tr -d '\r')

if [[ "${remaining:-0}" -le 0 ]]; then
  reset_time=$(date -d "@$reset_epoch" +"%Y-%m-%d %H:%M:%S")
  echo "❌ GitHub API rate limit exceeded. Limit resets at $reset_time UTC."
  exit 1
fi

# --- Parse asset names ---
ASSETS=$(jq -r '.assets[].name' "$response")
echo "Assets in latest release:"
echo "$ASSETS"

# --- Try to match a binary for this platform ---
MATCH=$(echo "$ASSETS" | grep -i "$MATCH_SUFFIX" || true)

if [[ -n "$MATCH" ]]; then
  echo -e "\n✅ Found a matching binary: $MATCH"
  read -rp "Download this file? [y/N] " yn
  if [[ "$yn" =~ ^[Yy]$ ]]; then
    URL=$(jq -r --arg NAME "$MATCH" '.assets[] | select(.name == $NAME) | .browser_download_url' "$response")
    echo "Downloading $URL"
    curl -L -o "$MATCH" "$URL"
    echo "Saved to ./$MATCH"
  else
    echo "Skipped download."
  fi
else
  echo -e "\n⚠️  No matching asset found for suffix '$MATCH_SUFFIX'"
fi

# Cleanup
rm -f "$response" "$headers"
