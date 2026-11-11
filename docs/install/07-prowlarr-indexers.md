# 7. Search sources in Prowlarr

The project ships no search sources ("indexers" in Prowlarr). You add the ones you are allowed to use, in
Prowlarr's own UI. Any account details you enter stay in Prowlarr's database on the server and never reach a
client.

1. Open Prowlarr's UI at `http://<server>:<PROWLARR_PORT>/`. By default it listens on the server only
   (`PROWLARR_BIND=127.0.0.1`): open it on the server itself, or set `PROWLARR_BIND` to the server's LAN
   address in `.env` and run `docker compose up -d prowlarr`.
2. On the first visit Prowlarr shows **Authentication Required**. Choose **Forms (Login Page)**, set a
   username and password, and save. "Authentication Required" stays **Enabled**: the stack fixes it, so the
   login applies to LAN addresses too.
3. Go to **Indexers → Add Indexer**. The **Language** and **Privacy** filters narrow the list; **Public** ones need
   no account. Fill in the settings (and your account details where one is needed), **Test**, then **Save**.
4. Repeat for each source. Two or three that cover your languages are enough to start.

## Sources behind Cloudflare (optional)

If a source's **Test** fails with "blocked by CloudFlare Protection" and the source's terms allow automated
access, FlareSolverr can answer its browser check:

1. Start it: add the `flaresolverr` profile (`docker compose --profile flaresolverr up -d`). In Portainer, set
   the stack variable `COMPOSE_PROFILES=flaresolverr` and redeploy.
2. In Prowlarr, **Settings → Tags**: add a tag `flaresolverr`.
3. **Settings → Indexers → Add (+) → FlareSolverr**: Host `http://flaresolverr:8191/`, Tags `flaresolverr`, Save.
4. Edit each blocked source, add the tag `flaresolverr`, **Test**, **Save**. Tag only the sources that need it.

The first search after a restart is slow (up to a minute) while the browser checks are answered.

## Things to know

* Use only sources you may use, with your own account where one is needed, and follow each source's terms. The
  streaming engine never uploads.
* Some sources offer a "use magnet links" option. Either setting works: clients only ever see the gateway's own
  release links, and the gateway fetches the release from Prowlarr itself, so no Prowlarr key reaches a client
  (an automated test checks this).
* The gateway talks to Prowlarr with Prowlarr's API key, which the stack sets from `.env`. You do not need to
  copy it anywhere.

## Check

Open a film in Lampa and its release list (Lampa labels it **Torrents**). Results come in the gateway's order,
best first: your audio language, resolution and HDR setting (Lampa's **Settings → Release search**), then
voice-over type and availability. Each release shows the resolution and voice-over the gateway's parser found;
Lampa's **Filter** narrows by quality, voice-over and season. An empty list with no error means no source found
the title; see [troubleshooting](../troubleshooting.md#search-returns-nothing).
