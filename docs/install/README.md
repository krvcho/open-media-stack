# Install guide

> Tested on a Raspberry Pi 5 (with Portainer) and on Windows 11 with Docker Desktop, where a fresh install took
> 5 minutes from `git clone` to playback.

Target: a working stack in under an hour on a Raspberry Pi 5 or an amd64 Linux host with Docker.

| Step | What | Time |
|---|---|---|
| [1. Prerequisites](01-prerequisites.md) | Hardware, OS, what you need before starting | 5 min |
| [2. Tailscale](02-tailscale.md) (optional) | Tailscale on the server and the phone, for remote access | 5 min |
| [3. Docker](03-docker.md) | Docker Engine and Compose v2 | 0–15 min |
| [4. TMDB key](04-tmdb-key.md) | Your own TMDB API key | 5 min |
| [5. Clone and configure](05-clone-and-configure.md) | `git clone`, `.env` | 10 min |
| [6. First run](06-first-run.md) | Start the stack, check health | 10 min |
| [7. Search sources](07-prowlarr-indexers.md) | Add the search sources you may use, in Prowlarr | 10 min |
| [8. Connect clients](08-connect-clients.md) | Browser, iPhone, Android TV, Samsung, LG | 5 min per device |
| [9. Monitoring](09-monitoring.md) (optional) | Prometheus, Loki, Grafana and Alloy as a separate stack | 10 min |
| [10. Backup and restore](10-backup-restore.md) | Save and bring back profiles, history, indexers and `.env` | 5 min |

On a Windows 11 PC with Docker Desktop, read [Windows 11](windows.md) alongside these steps.

The TMDB key comes before configuration because the gateway needs it at first start.

Throughout the guide:

* `<server>` is the server's LAN address or hostname, for example `192.0.2.10` (an example address).
* `<token>` is the household access token from your `.env`.
* Ports come from your `.env`; the defaults are 8080 (gateway), 9118 (Lampac) and 9696 (Prowlarr).
