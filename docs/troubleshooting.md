# Troubleshooting

Common problems, each with its cause and fix. Start with `docker compose ps` and `docker compose logs <service>`.

## Playback

### A series returns to the file list after each episode on Android
* **Cause:** the Lampa Android app passes the season to MX Player, ViMu, Just+ Player, UPlayer and DDD Player
  only. Just Player and other players get one file, and the app does not start the next episode itself.
  Online sources may still go on, when they play in Lampa's own player.
* **Fix:** choose MX Player (or another player from that list) in the app's player choice.

### An online source does not play, or stops after a few minutes
* **Cause:** online sources are Lampac's third-party modules. When a site a module relies on changes, the module
  can stop working until a Lampac release updates it; the problem is in Lampac, not in this stack.
* **Fix:** pick another source in the list. To hide a broken one, list it in `LAMPAC_DISABLED_SOURCES` (names as
  Lampac's list shows them, comma-separated) and redeploy; remove it again once a Lampac release fixes the module
  (the image is pinned, so updating Lampac is a deliberate change).

### Video plays without sound in a desktop browser
* **Cause:** the release's audio is AC3, EAC3, DTS or TrueHD, which many browsers do not decode in HTML5
  video. The stack does not transcode.
* **Fix:** pick a release with AAC audio (the ranking prefers them for browsers), or open the stream in VLC or
  mpv through the playlist link.

### A TV plays video but some audio tracks are silent
* **Cause:** the TV model does not decode that codec (often DTS or TrueHD).
* **Fix:** switch to another audio track or pick another release. Per-model notes go in
  [requirements.md](requirements.md).

### Playback starts slowly or stalls
* **Cause:** the streaming engine never uploads, so other peers rarely favour it; speed depends on how many
  peers offer the release.
* **Fix:** pick a release that shows more availability. The gateway hides releases below a minimum. With
  monitoring on, the streaming panels in Grafana show the download rate.

## Clients

### The browser console shows requests "blocked by Content Security Policy"
* **Cause:** Lampa tries to load plugins and send ad and metric requests to the CUB domains, with no setting
  to turn this off. The gateway's Content-Security-Policy refuses them on purpose.
* **Fix:** none needed. Stock TV apps are not covered by this and keep contacting CUB.

### Plugins stopped working on a TV after a few days
* **Cause:** stock TV apps always load the newest upstream Lampa, and an upstream change broke a
  plugin.
* **Fix:** check the daily canary result in CI. Update the stack once a fix is released; the browser build is
  pinned and keeps working meanwhile.

### The browser cannot reach the server over HTTPS but works over HTTP, or the reverse
* **Cause:** mixing an HTTPS page with HTTP endpoints. On the LAN everything is HTTP; over Tailscale
  everything is HTTPS.
* **Fix:** use one address consistently: the LAN address at home, or the tailnet name remotely.

### Every request returns 404
* **Cause:** a wrong or old household token in the URL. The gateway answers 404, not 401, on purpose.
* **Fix:** check the token in `.env`, and re-enter the bootstrap plugin URL on the device after a token rotation.

## Search

### Search returns nothing
* **Cause:** no indexers configured in Prowlarr, an indexer failing (login, captcha, block), or no release
  matches the card.
* **Fix:** test the indexers in Prowlarr's UI, and check `gateway_search_results{stage="raw"}` against
  `stage="matched"` to see whether results were found but filtered out.

### Search fails or shows "no response" with a slow indexer
* **Cause:** Lampa gives up after its parser timeout, 15 s by default. Prowlarr waits for every enabled
  indexer, so one slow indexer delays all results. Internet Archive is very slow through Prowlarr (30–70 s),
  because archive.org throttles Prowlarr's User-Agent.
* **Fix:** in Lampa, **Settings → Parser**, raise the timeout (30–120 s), or disable the slow indexer in
  Prowlarr. The gateway itself waits up to 120 s.

### The gateway logs "indexer search failed … prowlarr search: HTTP 400"
Prowlarr answers 400 "Search failed due to all selected indexers being unavailable" while it has disabled an
indexer after failures (escalating back-off; **System → Status** and the indexer list show it). The gateway
asks each indexer on its own, so one disabled indexer gives one 400 per search; the others still answer.
Find the failure in Prowlarr's log (**System → Logs**). "Cloudflare protection detected" or HTTP 403 from a
`cloudflare` server means the indexer needs the `flaresolverr` tag ([step 7](install/07-prowlarr-indexers.md)).
A successful **Test** clears the back-off.

### The Android app's player fails with "3003 Source error"
The app skipped TorrServer and gave the player the .torrent file. The project's plugin turns on **Settings →
TorrServer → Built-in client**; if you see this error, check the plugin loaded (search goes to the gateway)
and that this setting is "Yes", then restart the app.

### A release is found but never starts playing
* **Cause:** the release is offered only through HTTP web seeds, which TorrServer ignores, so with no peers nothing
  downloads.
* **Fix:** pick a release that has peers.

## Server

### Lampac restarts with "SQLite Error 14: unable to open database file"
* **Cause:** the database volume is owned by root, but Lampac runs as uid 1000.
* **Fix:** Lampac's entrypoint in `compose.yaml` starts as root, hands `/lampac/database` to the `lampac` user
  and drops to it with `setpriv` before Lampac starts, on every start. If Lampac was already in a restart loop,
  recreate it: `docker compose up -d --force-recreate lampac`.

### A stopped `lampac-init` or `gateway-init` container is left over
* **Cause:** older versions of the stack used these one-shot containers; they are gone from `compose.yaml`.
* **Fix:** remove them: `docker compose up -d --remove-orphans`, or delete them in Portainer.

### Adding a profile shows "Could not save the profile"
* **Cause:** the gateway cannot write its data volume (`/data`), usually one first made by an older image with
  another owner. The gateway's log says `profiles … permission denied`.
* **Fix:** once, hand the volume to the gateway's user:
  `docker run --rm --user 0:0 --entrypoint chown -v <project>_gateway-data:/data <TorrServer image from compose.yaml> 65532:65532 /data`.
  New volumes are made with the right owner by the gateway image itself.

### The gateway stays "unhealthy" on first start; its log ends with "downloading lampa build"
* **Cause:** on first start the gateway downloads the pinned Lampa build as a GitHub archive and checks its
  SHA-256 before it serves anything. GitHub's archive endpoint sometimes takes minutes or answers 503 or 504
  for this repository, even while GitHub reports no incident.
* **Fix:** wait. A download that fails or takes longer than 5 minutes stops the gateway, and Docker restarts it
  for another try. To skip the download, copy `lampa/<commit>/` from another installation's `gateway-data`
  volume into this one and restart the gateway: the gateway uses a build that is already there.

### A container keeps restarting
* **Cause:** a memory limit is too low for your use, and the container is OOM-killed. TorrServer needs about
  1.2 GB per stream playing at the same time.
* **Fix:** check `docker inspect` for `OOMKilled`. Raise `TORRSERVER_MEM_LIMIT` in `.env`, or another service's
  `mem_limit` in `compose.yaml`.

### Bookmarks and progress do not sync; Lampac answers "Войдите в аккаунт" or "Добавьте … в init.conf"
* **Cause:** the device sends no Lampac id or an old one. Lampac serves sync only to `LAMPAC_HOUSEHOLD_ID`. After the id changes in `.env`, devices keep the old one until Lampa reloads its plugins.
* **Fix:** restart the Lampa app or reload the browser page. Check that `docker compose up -d` ran after the
  change, so the gateway and Lampac both have the new value.

### Online sources do not load
* **Cause:** `ONLINE_SOURCES` is off (the default), a provider changed and broke, or a provider needs headless
  Chromium, which is off on the Minimum tier.
* **Fix:** check the flag and Lampac's log (with the flag on it lists the online modules it loaded); update Lampac
  when a fix is released. After changing the flag, redeploy: Lampac is recreated with the new module list.
