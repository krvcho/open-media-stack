#!/bin/sh
# Create an env file from .env.example with fresh random secrets. Never overwrites an existing file.
# Usage: sh scripts/gen-env.sh [target]   (default target: .env in the repository root)
# A file for Portainer, for example: sh scripts/gen-env.sh secrets/server.env  (secrets/ is git-ignored)
set -eu
cd "$(dirname "$0")/.."
target=${1:-.env}

if [ -e "$target" ]; then
  echo "$target already exists; not touching it. Delete it first to regenerate every secret." >&2
  exit 1
fi
command -v openssl >/dev/null 2>&1 || { echo "openssl is required" >&2; exit 1; }

umask 077
mkdir -p "$(dirname "$target")"
sed -e "s/^HOUSEHOLD_TOKEN=\$/HOUSEHOLD_TOKEN=$(openssl rand -hex 32)/" \
    -e "s/^PROWLARR_API_KEY=\$/PROWLARR_API_KEY=$(openssl rand -hex 16)/" \
    -e "s/^LAMPAC_HOUSEHOLD_ID=\$/LAMPAC_HOUSEHOLD_ID=$(openssl rand -hex 16)/" \
    -e "s/^TORRSERVER_PASSWORD=\$/TORRSERVER_PASSWORD=$(openssl rand -hex 24)/" \
    .env.example > "$target"

if grep -qE '^(HOUSEHOLD_TOKEN|PROWLARR_API_KEY|LAMPAC_HOUSEHOLD_ID|TORRSERVER_PASSWORD)=$' "$target"; then
  echo "a secret was left empty; check .env.example" >&2
  rm -f "$target"
  exit 1
fi
echo "Wrote $target (readable by you only). Check the ports and TZ in it before the first start."
