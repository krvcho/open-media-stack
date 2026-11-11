#!/bin/sh
# Restore a backup made by scripts/backup.sh onto this Docker host.
#
#   sh scripts/restore.sh <archive.tar>
#
# Replaces the content of the stack's state volumes with the archive's, creating them if they do not exist yet
# (labelled as Compose's own, so a later deploy adopts them). The archive is checked in full before anything is
# stopped or touched. The stack's running containers stop for the restore and start again afterwards. .env is
# written next to the repository only if none is there yet; an existing one is left alone and the archived one
# is put beside it as .env.restored.
#
# Environment: PROJECT (default: the project named in the archive), ENV_FILE (default ./.env).
set -eu
export MSYS_NO_PATHCONV=1 # see backup.sh

archive=${1:?usage: sh scripts/restore.sh <archive.tar>}
ENV_FILE=${ENV_FILE:-.env}
HELPER="alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a"

work=$(mktemp -d)
running=""
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

tar -C "$work" -xf - < "$archive"
[ -f "$work/manifest" ] || { echo "$archive is not a media-stack backup (no manifest)" >&2; exit 1; }
PROJECT=${PROJECT:-$(sed -n 's/^project=//p' "$work/manifest")}
VOLUMES=$(sed -n 's/^volumes=//p' "$work/manifest")
for v in $VOLUMES; do
  gzip -t "$work/$v.tar.gz" 2>/dev/null || { echo "archive has no valid $v.tar.gz: nothing changed" >&2; exit 1; }
done
docker image inspect "$HELPER" >/dev/null 2>&1 || docker pull -q "$HELPER" >/dev/null

running=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT")
# shellcheck disable=SC2086 # one id per word
[ -z "$running" ] || docker stop $running >/dev/null

for v in $VOLUMES; do
  docker volume inspect "${PROJECT}_$v" >/dev/null 2>&1 ||
    docker volume create --label "com.docker.compose.project=$PROJECT" --label "com.docker.compose.volume=$v" \
      "${PROJECT}_$v" >/dev/null
  docker run --rm -i -v "${PROJECT}_$v:/v" "$HELPER" \
    sh -c 'find /v -mindepth 1 -delete && tar -C /v --numeric-owner -xzf -' < "$work/$v.tar.gz"
  echo "  $v"
done

if [ -f "$work/env" ]; then
  umask 077
  if [ -e "$ENV_FILE" ]; then
    cp "$work/env" "$ENV_FILE.restored"
    echo "  $ENV_FILE exists, left alone; the archived one is $ENV_FILE.restored"
  else
    cp "$work/env" "$ENV_FILE"
    echo "  $ENV_FILE"
  fi
fi
restart
running=""
echo "Restored into project $PROJECT. Start or redeploy the stack if it was not running."
