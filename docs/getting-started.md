# Getting started: from nothing to the first film

One page for someone who has never run a server. Every step says **what** you do, **why**, **how**, and what to
**check** before moving on. The numbered [install guide](install/README.md) has the details behind each step.

You need about an hour, most of it reading. The commands themselves took 5 minutes on a test machine.

Prefer to be guided? [Set it up with Claude Code](ai-setup.md): it asks you questions and does the typing.

---

## 1. Pick the machine that will be the server

**What.** A computer that stays on while you watch: a Raspberry Pi 5, a mini PC, a home server, or your desktop
(Windows 11 works through Docker Desktop).

**Why.** The server searches, streams and keeps everyone's history. TVs and phones only show it.

**How.** For one 1080p stream: 2 cores, 4 GB RAM, 10 GB free disk. For two streams at once (one of them 4K):
4 cores and 8 GB. A wired network to the TV helps for 4K. Nothing is transcoded, so no graphics card is needed
([requirements](requirements.md)).

**Check.** You know the server's address on your home network (for example `192.168.1.20`). On the server:
Linux `ip addr`, Windows `ipconfig`. Ask your router to always give it the same address (a "DHCP
reservation"), or every device will lose it after a restart.

## 2. Install Docker and Git

**What.** Docker runs each part of the stack in its own container; Git downloads the project.

**Why.** No part of the stack is installed on the machine itself, so it can be updated or removed cleanly.

**How.** Linux: Docker Engine with the Compose plugin ([step 3](install/03-docker.md)). Windows 11: Docker
Desktop and Git for Windows ([Windows guide](install/windows.md)). On a Raspberry Pi, also enable the memory
cgroup ([step 1](install/01-prerequisites.md)).

**Check.** `docker compose version` prints a version 2.x. `git --version` prints a version.

## 3. Download the project and create your settings

**What.** A copy of the project and a `.env` file with your own secrets.

**Why.** `.env` holds the household token (the "password" in every address), the keys the parts use to talk
to each other, and your choices. It never leaves the server; don't share it.

**How.** In a terminal (Git Bash on Windows):

```bash
git clone https://github.com/krvcho/open-media-stack.git media-stack
cd media-stack
sh scripts/gen-env.sh
```

Then open `.env` in a text editor and set `TZ` to your time zone. Optionally put your own TMDB key in
`TMDB_API_KEY` ([step 4](install/04-tmdb-key.md)): the catalogue works without it, but with it the server
caches TMDB for every device.

**Check.** `.env` exists and `HOUSEHOLD_TOKEN` is a long random string.

## 4. Decide about a VPN before the first playback

**What.** Whether the streaming engine's peer-to-peer traffic leaves through a VPN or through your home connection.

**Why.** Privacy. The streaming engine never uploads to others, but it takes part in peer-to-peer networks,
where the other participants see the public IP address it connects from. Through a VPN they see the VPN's
address instead of your home one. A VPN does not change what you may lawfully watch.

**How.** If yes: a VPN provider that allows peer-to-peer traffic and offers a WireGuard configuration file. Copy its
fields into the `WIREGUARD_*` lines of `.env`, set `TORRSERVER_EGRESS=vpn` and `COMPOSE_PROFILES=vpn`
([VPN egress](install/05-clone-and-configure.md#vpn-egress-for-the-streaming-engine)). If the tunnel drops,
streaming stops instead of falling back to your address.

**Check.** After step 5: `docker compose logs vpn` shows the provider's address, and Lampa's top bar later shows
a green shield. Orange means no VPN is in use.

## 5. Start the stack

**What.** Download the parts and start them.

**Why.** From here on everything runs on the server by itself, and starts again after a reboot.

**How.**

```bash
docker compose up -d --build
```

The first start downloads about 3.5 GB and builds one small part; a few minutes on a fast connection.

**Check.** `docker compose ps`: `gateway` and `lampac` say `healthy`, the rest `running`. Open
`http://<server>:8080/h/<HOUSEHOLD_TOKEN>/` in a browser on the server or another computer: Lampa opens and the
home screen fills with posters. If not, see [troubleshooting](troubleshooting.md).

## 6. Add where releases are searched (Prowlarr)

**What.** Indexers: the sites the stack searches for releases. **The project ships none**; until you add at
least one, a film's release list stays empty. (Lampa labels that list **Torrents**.)

**Why.** Which sites you may use, and what content you may watch, depends on the law where you live, on the
rights of the content's owners and on each site's terms. That choice and that responsibility are yours, so the
project names none.

**How.** Open Prowlarr at `http://<server>:9696/` (on the server itself; see [step 7](install/07-prowlarr-indexers.md)
to open it from another computer).

1. First visit: choose **Forms (Login Page)**, set a username and password for Prowlarr, save.
2. **Indexers → Add Indexer.** Use the filters: **Language** for the languages you watch in, **Privacy: Public**
   for sites that need no account. For a site that needs an account, use your own and follow its terms.
3. Pick one, **Test**, **Save**. Two or three that cover your languages are enough to start.
4. If **Test** says the site is behind a browser check (Cloudflare) and its terms allow automated access: turn on
   the `flaresolverr` profile and tag that indexer
   ([step 7](install/07-prowlarr-indexers.md#sources-behind-cloudflare-optional)).

**Check.** In Lampa open a film and its release list: releases appear within a few seconds, the ones in your
language and resolution first. For a first test with something you may watch, pick a film that is in the
public domain where you live (many early films are; the rules differ between countries) and play it.

Optional: **online sources**, Lampac's own third-party modules, off by default. `ONLINE_SOURCES=1` in `.env`
turns them on and films get an **Online** button. The same rule applies: you decide whether you may use what they
reach.

## 7. Choose your preferences

**What.** In Lampa: **Settings → Release search** ("Поиск релизов"): audio language, quality, and whether the
screen supports HDR.

**Why.** The server sorts releases by these, so the best fit is first and you rarely scroll.

**Check.** The order of a film's releases changes when you change the language.

## 8. Connect the TV and the phones

**What.** Each device opens the same household address.

**Why.** The address carries the token: no accounts, and every device shares profiles and history.

**How.** ([step 8](install/08-connect-clients.md) has every platform.)

* **Browser:** `http://<server>:8080/h/<token>/`. Bookmark it.
* **Android TV / Google TV:** install the Lampa app for Android TV from its author's channels. On first start it
  asks for the Lampa address: enter `http://<server>:8080/h/<token>/`. Typing it with a remote is slow; the
  Google TV phone app's remote keyboard accepts pasted text. Then install an external video player that can
  play a series as a playlist (step 8 lists the ones the app supports) and choose it as the player.
* **Samsung / LG:** install Lampa from the TV's store, then add the plugin
  `http://<server>:8080/h/<token>/plugins/modification.js` in Lampa's extensions (untested by the project).
* **iPhone:** open the address in Safari, **Share → Add to Home Screen**.

**Check** on each device: the "Who's watching?" screen appears; a film plays; for a series the next episode
starts by itself; stop half-way, reopen, and it resumes there; another device shows the same progress.

## 9. Keep it safe and running

* **Back up** profiles, history and indexers now and then: `sh scripts/backup.sh` ([step 10](install/10-backup-restore.md)).
  The backup holds secrets: keep it encrypted.
* **Away from home:** use Tailscale rather than opening ports on the router ([step 2](install/02-tailscale.md)).
  Nothing here is meant to be reachable from the internet.
* **Updates:** `git pull`, then `docker compose up -d --build`.
* **Optional monitoring** with dashboards and phone alerts: [step 9](install/09-monitoring.md).

## You are done when

- [ ] Lampa opens on every device and shows posters.
- [ ] A film's release list has releases in your language.
- [ ] A film plays, and a series moves on to the next episode by itself.
- [ ] The shield in the top bar is the colour you expect (green with a VPN).
- [ ] Progress made on one device shows on another.
- [ ] A backup exists somewhere other than the server.
