# 9. Monitoring (optional)

Prometheus, Loki, Grafana, Grafana Alloy and Alertmanager as a stack of their own, with alerts to a
Telegram bot if you want them. It watches the whole server:
host metrics, every container's CPU, memory, network and disk, every container's logs, and any container that
opts in with labels, media-stack's gateway among them. Skip this step if you do not want it, or if you already
run Prometheus: point it at the gateway's metrics port instead.

Time: about 10 minutes, plus 5 for the Telegram bot. Memory: about 0.5 GiB for the five services.

## What it opens

| Port | Service | Reachable from |
|---|---|---|
| 3000 (`GRAFANA_PORT`) | Grafana | LAN and tailnet (`GRAFANA_BIND=127.0.0.1` for this machine only) |
| 9090 | Prometheus | this machine only |
| 3100 | Loki | this machine only |
| 12345 | Alloy's own UI | this machine only |
| 9093 | Alertmanager | the stack's own network only (Grafana shows it) |

Alloy reads the Docker socket and the host's file systems **read-only**. It can read the configuration of every
container on the host, including environment variables. Logs pass a redaction stage (API keys, tokens,
passwords, cookies, the household token in `/h/<token>/`) before Loki stores them.

## With Portainer

1. **Stacks → Add stack → Repository**. Name: `monitoring`. Repository URL: your clone of this repository.
   Compose path: `monitoring/compose.yaml`.
   Use **Repository**, not Web editor or Upload: the images are built from the folders next to
   `compose.yaml`, which only a Git stack has.
2. **Environment variables**: `GRAFANA_ADMIN_PASSWORD` (required; for example the output of
   `openssl rand -base64 18`). Optional: `GRAFANA_PORT`, `GRAFANA_BIND`, `METRICS_RETENTION`,
   `METRICS_RETENTION_SIZE`, `LOGS_RETENTION`, and the Telegram settings from [Alerts to Telegram](#alerts-to-telegram)
   (defaults in [`monitoring/.env.example`](../../monitoring/.env.example)).
3. **Deploy the stack**. Portainer builds the five images (pinned upstream images plus this repository's
   configuration) and starts them.

## With the Docker CLI

```bash
cd monitoring
cp .env.example .env
```

Set `GRAFANA_ADMIN_PASSWORD` in `.env`, then:

```bash
docker compose up -d --build
```

## Check it

1. Open `http://<server>:3000` and sign in as `admin` with your password.
2. **Dashboards → Monitoring → Host and containers**: the CPU, memory, disk and container panels fill within a
   minute.
   With media-stack running, **Dashboards → Monitoring → media-stack** shows upstream health, the SLOs
   (search p95, stream time to first byte, time to first frame, availability), streaming, playback in
   Lampa's own player (time to first frame, stalls, errors), search and TMDB. Media-stack's gateway already
   carries the scrape labels; nothing to configure. Time to first frame and stalls come from Lampa's own player
   only (browser, Samsung, LG); external players on Android and iPhone report nothing back.
3. **Explore → Loki**, query `{project="media-stack"}`: the stack's logs, with `<redacted>` in place of tokens.
4. **Alerting → Alert rules**: the host and media-stack rules from `monitoring/prometheus/rules/` appear under
   the Prometheus data source. The most important one, `TorrentUploadDetected`, fires if TorrServer ever sends
   piece data to a peer. **Alerting → Silences** (Alertmanager data source) shows what is firing and mutes it.
   Without a Telegram bot, alerts stay in Grafana.

## Let another stack be scraped

Give the container two labels; no port needs publishing, Alloy reaches it on its Docker network address:

```yaml
labels:
  prometheus.io/scrape: "true"
  prometheus.io/port: "9100"
  # prometheus.io/path: /metrics   (the default)
```

The series get `job` = the Compose service, `project` = the Compose project, `instance` = the container name.

## Add a Windows PC

[windows_exporter](https://github.com/prometheus-community/windows_exporter) runs on the PC as a service; Alloy
on the server scrapes it over the LAN. Use the server's LAN address below as `<server>` and the PC's as `<pc>`
(give the PC a fixed address in your router, or the target breaks when DHCP hands it a new one).

1. On the PC, in PowerShell **as Administrator**, download version 0.32.0 and check it against the published
   SHA-256 (`d48ebad7999aac5603fe01007016a0ddbcecbc1aa0ecbc51867a2416b6c60a7d`):

   ```powershell
   $msi = "$env:TEMP\windows_exporter-0.32.0-amd64.msi"
   Invoke-WebRequest https://github.com/prometheus-community/windows_exporter/releases/download/v0.32.0/windows_exporter-0.32.0-amd64.msi -OutFile $msi
   (Get-FileHash $msi -Algorithm SHA256).Hash
   ```

2. Install it with a firewall rule that admits only the server:

   ```powershell
   msiexec /i $msi --% ADDLOCAL=FirewallException REMOTE_ADDR=<server> ENABLED_COLLECTORS=cpu,cpu_info,gpu,logical_disk,memory,net,os,physical_disk,system
   ```

3. Check from the PC: `http://localhost:9182/metrics` lists `windows_` metrics.
4. In the monitoring stack's environment (Portainer: the stack's environment variables) set, then redeploy:

   ```
   EXTRA_TARGETS=[{"__address__":"<pc>:9182","instance":"pc","job":"windows"}]
   ```

5. **Dashboards → Monitoring → Windows PC** fills within a minute. While the PC sleeps or is off, its target is
   down (`up{job="windows"} == 0`); the ScrapeTargetDown alert ignores `job="windows"` for that reason.

## Watch a service that has no metrics

Alloy can request an address every 30 seconds and alert when it stops answering. It runs in the host's network,
so a port bound to `127.0.0.1` works. In the monitoring stack's environment set, then redeploy:

```
HTTP_PROBES=[{"__address__":"http://127.0.0.1:8085/health","name":"notes","module":"http_2xx"}]
```

Any answer other than 2xx within 5 seconds counts as a failure. The series are `probe_success{job="probe",
instance="notes"}` and `probe_duration_seconds`; the `ProbeFailed` alert (critical) fires after 3 minutes of
failures. Several addresses go in the same list.

## Watch a backup job

A job on the host can report its own success, and the `BackupStale` alert (warning) fires when the last success
is more than 26 hours old.

1. Create a directory for such files on the host, and in the monitoring stack's environment point Alloy at it
   (the host path with `/rootfs` in front), then redeploy:

   ```sh
   sudo install -d -m 0755 /var/lib/prometheus/node-exporter
   ```

   ```
   TEXTFILE_DIR=/rootfs/var/lib/prometheus/node-exporter
   ```

2. At the end of the backup script, after everything succeeded, write one line into a `.prom` file there
   (the job's user needs write access to that file):

   ```sh
   printf 'backup_last_success_timestamp_seconds{name="notes"} %s\n' "$(date +%s)" \
     > /var/lib/prometheus/node-exporter/notes_backup.prom
   ```

3. After the next run, **Explore → Prometheus**, query `time() - backup_last_success_timestamp_seconds`: the age
   of each backup in seconds. A job that has never written the file has no series, so it raises no alert: run it
   once and check the query.

## Alerts to Telegram

What gets sent:

| Severity | Examples | Message |
|---|---|---|
| 🔴 critical | TorrServer uploaded to peers; VPN, TorrServer or Prowlarr down 5 min; a scrape target down; disk under 10 % free; no host metrics | at once with sound, again every 2 h, ✅ when resolved |
| 🟠 warning | Lampac down; settings drift; many failed searches; memory pressure; a hot sensor; a container near its memory limit; a Windows PC drive under 10 % free | silent, not between 23:00 and 08:00, again every 12 h |
| ⚪ info | slow searches | Grafana only |
| 💓 heartbeat | always | once a day at 10:00, silent; no message means monitoring is down |

1. In Telegram, open **@BotFather**, send `/newbot`, pick a name and a user name ending in `bot`. It answers
   with the bot token (`123456789:AA…`). Keep it secret: anyone with it can post as the bot.
2. Open a chat with your new bot and send it any message (a bot cannot write first). For a group: add the bot
   to the group and post a message there.
3. Find the chat id: open `https://api.telegram.org/bot<token>/getUpdates` in a browser and read
   `"chat":{"id":…}`. A private chat id is a positive number; a group's is negative.
4. Set in the stack's environment (Portainer: **Stacks → monitoring → Editor → Environment variables**, or
   `monitoring/.env`):

   | Variable | Example |
   |---|---|
   | `TELEGRAM_BOT_TOKEN` | the token from step 1 |
   | `TELEGRAM_CHAT_ID` | the id from step 3 |
   | `ALERT_TIMEZONE` | `Europe/Warsaw` (quiet hours and the heartbeat; default UTC) |
   | `GRAFANA_URL` | `http://<server>:3000` (a link under every message; optional) |

5. Redeploy the stack. Send a test alert; it arrives within a minute, with sound:

   ```bash
   docker exec monitoring-alertmanager-1 amtool --alertmanager.url=http://localhost:9093 alert add alertname=TestAlert severity=critical --annotation=summary="Test from the monitoring stack"
   ```

   It resolves by itself 5 to 10 minutes later (✅ message).

If nothing arrives: `docker logs monitoring-alertmanager-1` names the problem (a wrong token gives
`Unauthorized`, a wrong chat id `chat not found`, a chat with no message from you `bot can't initiate`). Quiet
hours and the heartbeat time are in `monitoring/alertmanager/alertmanager.yml`.

## Disk use

| What | Limit |
|---|---|
| Metrics (Prometheus) | `METRICS_RETENTION` (15 days) or `METRICS_RETENTION_SIZE` (5 GB), whichever comes first |
| Logs in Loki | `LOGS_RETENTION` (7 days); Loki has no size limit |
| Docker's own log files of this stack's and media-stack's containers | 3 × 10 MB per container (`logging` in each `compose.yaml`) |

Docker keeps the log files of **other** containers on the host without a limit unless their own Compose file or
the daemon sets one (`docker inspect <name> --format '{{.HostConfig.LogConfig}}'` shows it). To cap them for every container, put this in `/etc/docker/daemon.json` (needs root), restart Docker,
and recreate the containers (the setting applies to new containers only):

```json
{ "log-driver": "json-file", "log-opts": { "max-size": "10m", "max-file": "3" } }
```

The `HostDiskAlmostFull` alert fires below 10 % free on any disk.

## Update or remove

Redeploy the stack after pulling a new version (Portainer: **Pull and redeploy**). Removing the stack keeps its
volumes (`monitoring_prometheus-data`, `monitoring_loki-data`, `monitoring_grafana-data`,
`monitoring_alloy-data`, `monitoring_alertmanager-data`); delete them to drop the history. Media-stack does not depend on this stack.
