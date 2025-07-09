#!/bin/bash

# Template variables ----------------------------------------------------------

# Replace with app name.
APP_NAME="goweb"

# Startup ---------------------------------------------------------------------

set -euo pipefail
umask 022

# dep check. Thanks to go's cross-compilation, we can skip the platform check and just do this.
required_bins=(go sed awk gzip)
for bin in "${required_bins[@]}"; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "error: '$bin' is required but not installed or not in \$PATH" >&2
    exit 1
  fi
done

version="vX.X.X" # default / development version
BIN_DIR=bin
RELEASE_BODY_FILE="$BIN_DIR/release_body.md"

# clean bin dir
rm -rf "$BIN_DIR" && mkdir -p "$BIN_DIR"
echo "🟢 Cleaned bin directory"

# if running in CI, extract latest version and description from CHANGELOG.md, if tag already exists, flag and exit.
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  echo "Building for CI..."
  version=$(sed -n 's/^## \[\(.*\)\] - .*/\1/p' CHANGELOG.md | head -n 1)
  description=$(awk '/^## \['"$version"'\]/ {flag=1; next} /^## \[/ {flag=0} flag {print}' CHANGELOG.md)
  echo "$description" > "$RELEASE_BODY_FILE"

  # check tag already exists
  if git tag -l "$version" | grep -q "$version"; then
    echo "Version $version is already tagged."
    echo "DRAFT_RELEASE=false" >> $GITHUB_ENV
    exit 0
  else
    echo "DRAFT_RELEASE=true" >> $GITHUB_ENV
    echo "VERSION=$version" >> $GITHUB_ENV
  fi
fi

LDFLAGS="-X 'main.Version=$version'"
GO_MAIN_PATH="./go/main"

# place any other pre-build steps here e.g.:
# - linting
# - formatting
# - tailwindcss
# - tests
# - etc.

# build
build_command="GOOS=linux GOARCH=amd64 go build -ldflags=\"$LDFLAGS\" -o \"$BIN_DIR/linux-amd64\" \"$GO_MAIN_PATH\""
eval "$build_command"
echo "🟢 Built $BIN_DIR/linux-amd64"

# gzip
gzip -c -- "$BIN_DIR/linux-amd64" > "$BIN_DIR/linux-amd64.gz"
echo "🟢 Gzipped $BIN_DIR/linux-amd64"