#!/bin/sh
# Alertmanager reads no environment variables, so the Telegram settings from the stack's environment are written
# here, in /tmp of this container only. Without a bot token every alert stays in Grafana and nothing is sent.
set -eu
umask 077
src=/etc/alertmanager

if [ -z "${TELEGRAM_BOT_TOKEN:-}" ]; then
  echo "TELEGRAM_BOT_TOKEN is empty: alerts are shown in Grafana only" >&2
  printf 'route:\n  receiver: none\nreceivers:\n  - name: none\n' > /tmp/alertmanager.yml
else
  printf '%s' "${TELEGRAM_CHAT_ID:-}" | grep -Eqx -- '-?[0-9]+' || { echo "TELEGRAM_CHAT_ID must be a number" >&2; exit 1; }
  tz=${ALERT_TIMEZONE:-UTC}
  printf '%s' "$tz" | grep -Eqx '[A-Za-z0-9_+/-]+' || { echo "ALERT_TIMEZONE must be a zone name such as Europe/Warsaw" >&2; exit 1; }
  url=${GRAFANA_URL:-}
  [ -z "$url" ] || printf '%s' "$url" | grep -Eqx 'https?://[A-Za-z0-9.:/_-]+' || { echo "GRAFANA_URL must look like http://host:3000" >&2; exit 1; }

  printf '%s' "$TELEGRAM_BOT_TOKEN" > /tmp/telegram-token
  sed -e "s|__TZ__|$tz|g" -e "s|__CHAT_ID__|$TELEGRAM_CHAT_ID|g" "$src/alertmanager.yml" > /tmp/alertmanager.yml
  if [ -n "$url" ]; then
    sed "s|__GRAFANA_URL__|$url|g" "$src/telegram.tmpl" > /tmp/telegram.tmpl
  else
    grep -v __GRAFANA_URL__ "$src/telegram.tmpl" > /tmp/telegram.tmpl
  fi
fi

# Flags go to Alertmanager; any other command (amtool in CI) runs as is, after the files above exist.
case "${1:-}" in
  -*) exec /bin/alertmanager "$@" ;;
  *) exec "$@" ;;
esac
