# Requirements

> Hardware numbers are measured on a Raspberry Pi 5 and an amd64 PC.

## Hardware

Reference hardware: Raspberry Pi 5, 16 GB RAM, arm64, NVMe RAID1, shared with other self-hosted services.
The stack also targets generic amd64 hosts.

| Tier | Contents | Target | Measured requirement |
|---|---|---|---|
| Minimum | gateway, TorrServer, Prowlarr, Lampac (sync only) | 1 concurrent 1080p stream | 2 cores, 4 GB RAM, 10 GB disk, internet ≥ 25 Mbit/s |
| Recommended | Minimum + online sources | 2 concurrent streams (one 2160p) + online playback | 4 cores, 8 GB RAM, 10 GB disk, internet 100 Mbit/s for remuxes, wired LAN to the TV |
| Full | Recommended + the monitoring stack | Recommended with dashboards and alerts | Recommended + 1 GB RAM, 2 GB disk |

* TorrServer needs about 1.2 GB of RAM per concurrent stream (its cache is per torrent); its limit,
  `TORRSERVER_MEM_LIMIT`, is 3200 MB for two. FlareSolverr, if an indexer needs it, adds about 2.6 GB.
* On the reference Pi 5 (16 GB) a 2160p UHD remux (77 Mbit/s) and a 1080p stream at once used at most 46 % of the
  CPU, 2.4 GB for TorrServer, and the SoC stayed at 55 °C without throttling.
* Every service has a memory limit in `compose.yaml`: its measured peak plus 30 %.

Notes:

* No video transcoding happens anywhere in the stack, so no hardware encoder is needed.
* TorrServer keeps its cache in RAM only. Disk space is needed for container images, application
  databases, and logs and metrics if the monitoring profile is on, not for media.

## Software

* Docker Engine with the Compose v2 plugin (Compose profiles are used). Check with `docker version` and
  `docker compose version`.
* Linux on arm64 or amd64.
* Optional: Portainer CE to manage the stack as a Git stack (tested with Portainer CE 2.45, profiles included).
* `git` to clone the repository.

## Network

* A LAN shared by the server and the TV clients. TVs connect over the LAN only (they have no Tailscale client).
* Outbound internet from the server: GitHub (Lampa web build, first start), TMDB API, the indexers you
  configure, the streaming engine's peers, and online sources if enabled.
* Outbound internet from TV clients: the stock Lampa apps load the app from `yumata.github.io` at every start, and posters load from `image.tmdb.org`. Stock apps also contact the CUB domains (plugins,
  metrics, ads) on their own; the browser build served by the gateway blocks this with a
  Content-Security-Policy, stock apps do not.
* Optional: a Tailscale tailnet for remote access from phones and laptops.
* No inbound ports from the internet, and no router changes (UPnP is off).
* Host ports for the gateway, Lampac and Prowlarr's UI are configurable in `.env` to avoid collisions with
  other stacks.

## Accounts and keys

| What | Required | Notes |
|---|---|---|
| TMDB API key | **yes** | Free; see [install step 4](install/04-tmdb-key.md) |
| Indexer accounts | for private or semi-private indexers | Added by you in Prowlarr; the repo ships none |
| Tailscale account, with Tailscale on the server and the phone | for remote access only | [install step 2](install/02-tailscale.md) |

## Client platforms

The matrix comes from a test set of twelve releases covering common formats; platforms not available for testing are marked untested.

| Platform | Lampa app | Player | Video/audio formats | Audio switching | Subtitles |
|---|---|---|---|---|---|
| Android TV (tested: Chromecast with Google TV 4K) | stock APK, Lampa loaded from the gateway | the app's own player, or an external app through the system chooser | all 12 matrix cells play (2026-11-10) | yes, during playback | yes, during playback |
| Samsung Tizen | stock app | Lampa inner (AVPlay) | untested (no device available) | untested | untested |
| LG webOS | stock app | Lampa inner or system player | untested (no device available) | untested | untested |
| Desktop browser | served by the gateway | HTML5 video | H.264 with AAC plays (Chrome, 2026-11-11); HEVC, AC3/EAC3, DTS and TrueHD depend on the browser and often fail ([troubleshooting](troubleshooting.md)) | not checked per browser | not checked per browser |
| iPhone (tested on LAN and Tailscale) | Safari or a Home Screen web app, served by the gateway | Safari's player, or an external app (VLC, Infuse, nPlayer, …) | plays (2026-11-10); per-format not tested | not tested | not tested |

### Playback matrix

Twelve real releases, each probed with ffprobe through TorrServer (`/ffp/`) so the formats are measured, not
guessed from titles. The release names stay out of the repository. Each cell: plays / sound / audio
switch / subtitle switch. Run on 2026-11-10: everything works (reported as a whole, not cell by cell;
how Dolby Vision 7 is shown, as DV or as its HDR10 base, was not recorded).

| # | Formats | Chromecast 4K, app player | Chromecast 4K, Just Player |
|---|---|---|---|
| 1 | MKV, H.264 1080p, EAC3 5.1 + AC3 2.0 (2 languages), SRT | works | works |
| 2 | MKV, HEVC 1080p SDR, AC3 5.1 × 2, SRT | works | works |
| 3 | MKV, HEVC HDR10 (no DV), AC3 × 3 incl. Ukrainian, SRT; several files | works | works |
| 4 | MKV, HEVC 2160p Dolby Vision 8, EAC3/AC3/AAC, 5 voice-overs, SRT | works | works |
| 5 | MKV, HEVC 2160p Dolby Vision 7 (BD remux), EAC3/AC3, PGS + SRT | works | works |
| 6 | MP4, HEVC 2160p Dolby Vision 5, EAC3/AC3, mov_text; series | works | works |
| 7 | MKV, HEVC 2160p DV8, DTS-HD MA 7.1 + EAC3, PGS | works | works |
| 8 | MKV, HEVC 2160p DV8, AC3 × 3 + DTS 5.1 × 2 (Russian voice-overs), PGS + SRT | works | works |
| 9 | MKV, HEVC 2160p DV7, TrueHD Atmos 7.1 + AC3, PGS + SRT | works | works |
| 10 | MKV, H.264 1080p, EAC3 Atmos + EAC3/AC3 (5 voice-overs), ASS + SRT; series | works | works |
| 11 | MKV, HEVC 2160p SDR, Opus 2.0, styled ASS (anime) | works | works |
| 12 | MKV, HEVC 2160p SDR, AAC 5.1, PGS + SRT | works | works |
