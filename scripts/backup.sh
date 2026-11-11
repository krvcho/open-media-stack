#!/bin/sh
# Back up media-stack's state: the named volumes that cannot be downloaded again, plus .env.
#
#   sh scripts/backup.sh [output-dir]          default: ./backups
#
# Run it on the Docker host, from the repository folder (Docker CLI only; works for a Portainer stack too).
# The stack's containers stop for the few seconds the archive takes and start again afterwards.
# THE ARCHIVE HOLDS SECRETS: the household token, TorrServer and Prowlarr keys, and your trackers' logins in
# Prowlarr. Keep it encrypted (for example `gpg -c <archive>`) and off shared storage.
#
# Environment: PROJECT (Compose project name, default media-stack), ENV_FILE (default ./.env; a Portainer stack
# keeps its variables in Portainer: copy them into a file and point ENV_FILE at it).
set -eu
# Volume data travels through stdin/stdout only, never a host path; this keeps Git Bash on Windows from
# rewriting the container paths below.
export MSYS_NO_PATHCONV=1

PROJECT=${PROJECT:-media-stack}
ENV_FILE=${ENV_FILE:-.env}
OUT=${1:-backups}
VOLUMES="gateway-data torrserver-config prowlarr-config lampac-database"
HELPER="alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a"

for v in $VOLUMES; do
  docker volume inspect "${PROJECT}_$v" >/dev/null 2>&1 || { echo "volume ${PROJECT}_$v not found (PROJECT=$PROJECT?)" >&2; exit 1; }
done
docker image inspect "$HELPER" >/dev/null 2>&1 || docker pull -q "$HELPER" >/dev/null

stamp=$(date +%Y%m%d-%H%M%S)
work=$(mktemp -d)
mkdir -p "$OUT"
archive="$OUT/$PROJECT-backup-$stamp.tar"

running=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT")
# Start the stopped containers again. A container that joins another's network (TorrServer joins torrserver-net
# or the VPN) fails until that one runs, so whatever failed is retried after the others.
restart() {
  left=$running
  for _ in 1 2 3 4; do
    [ -n "$left" ] || return 0
    failed=""
    for id in $left; do docker start "$id" >/dev/null 2>&1 || failed="$failed $id"; done
    left=$failed
  done
  echo "could not start again:$left" >&2
  return 1
}
trap 'restart || true; rm -rf "$work"' EXIT
# shellcheck disable=SC2086 # one id per word
[ -z "$running" ] || docker stop $running >/dev/null

for v in $VOLUMES; do
  docker run --rm -v "${PROJECT}_$v:/v:ro" "$HELPER" tar -C /v --numeric-owner -czf - . > "$work/$v.tar.gz"
  [ -s "$work/$v.tar.gz" ] || { echo "empty archive for $v" >&2; exit 1; }
  echo "  $v ($(du -h "$work/$v.tar.gz" | cut -f1))"
done
restart || true # the archive is still written; restart names what did not come back
running=""

if [ -f "$ENV_FILE" ]; then
  cp "$ENV_FILE" "$work/env"
  echo "  .env ($ENV_FILE)"
else
  echo "  no $ENV_FILE: archive has the volumes only; keep your stack variables yourself" >&2
fi
printf 'project=%s\ncreated=%s\nvolumes=%s\n' "$PROJECT" "$stamp" "$VOLUMES" > "$work/manifest"

umask 077
tar -C "$work" -cf - . > "$archive" # a stream: a path with ":" is no remote host to tar
echo "Backup written: $archive ($(du -h "$archive" | cut -f1)). It contains secrets: store it encrypted."
