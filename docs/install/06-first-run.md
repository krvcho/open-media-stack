# 6. First run

From the repository directory:

```bash
docker compose up -d --build
```

The first start pulls the upstream images, builds the gateway image, and the gateway downloads the pinned Lampa
web build from GitHub and checks its SHA-256.

Check that every service is up:

```bash
docker compose ps
```

`gateway` and `lampac` should show `healthy`; every other service `running`.

Open Lampa in a desktop browser:

```
http://<server>:<GATEWAY_PORT>/h/<HOUSEHOLD_TOKEN>/
```

Choose the interface language; the home screen fills with TMDB rows.

## What healthy logs look like

```bash
docker compose logs gateway
```

The gateway logs JSON lines; on first start you see `downloading lampa build`, then `listening` for `:8080`
and `:8081`. It never logs URLs.

```bash
docker compose logs lampac
```

Lampac lists `loaded module:` lines for `SyncEvents`, `Storage`, `TimeCode` and `Sync`.

## Optional: the local test seeder

To check search and playback before adding real indexers, start the test seeder. It is a generated test clip,
a tracker and a one-item Torznab feed, all inside the stack:

```bash
docker compose --profile test up -d --build seeder
```

Then add it in Prowlarr as **Generic Torznab** with URL `http://seeder:9117` and API path `/api`
([step 7](07-prowlarr-indexers.md)). Any search from a Lampa card then lists
"Media Stack Test Clip (2026)", which plays in the browser.

If something fails, see [troubleshooting](../troubleshooting.md).
