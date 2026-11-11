# 5. Clone and configure

```bash
git clone https://github.com/krvcho/open-media-stack.git media-stack
```

```bash
cd media-stack
```

The folder is named `media-stack` on purpose: it is also the Compose project name, which the volumes carry.

## `.env`

All configuration and every secret live in `.env`, which is never committed. Create it
with fresh random secrets:

```bash
sh scripts/gen-env.sh
```

It refuses to overwrite an existing `.env`. Then review `.env`. Each variable is documented in `.env.example`: what it is, where to get it, whether it is
a secret.

| Variable | Secret | How to set it |
|---|---|---|
| `TMDB_API_KEY` | recommended | Your TMDB key or read access token ([step 4](04-tmdb-key.md)); the gateway proxies and caches TMDB |
| `HOUSEHOLD_TOKEN` | yes | `openssl rand -hex 32`. Part of every client URL |
| `PROWLARR_API_KEY` | yes | `openssl rand -hex 16`. Prowlarr adopts it at first start |
| `LAMPAC_HOUSEHOLD_ID` | yes | `openssl rand -hex 16`. Lampac serves sync only to requests that carry it; must differ from the token |
| `TORRSERVER_USER` | yes | Any name; `gateway` by default |
| `TORRSERVER_PASSWORD` | yes | `openssl rand -hex 24` |
| `TORRSERVER_CACHE_MB` | no | TorrServer's RAM cache, 512 by default |
| `TORRSERVER_UPLOAD_KB` | no | 0 (default) keeps upload off. A positive value allows upload capped at that many KB/s |
| `GATEWAY_PORT`, `LAMPAC_PORT`, `PROWLARR_PORT` | no | Change them if the defaults (8080, 9118, 9696) collide with other stacks |
| `PROWLARR_BIND` | no | `127.0.0.1` (host only, default) or the server's LAN address to reach Prowlarr's UI from another computer |
| `TZ` | no | Your time zone, e.g. `Europe/Warsaw` |


## Optional features

| Feature | How | Default |
|---|---|---|
| Remote access | Tailscale on the host, [step 2](02-tailscale.md); nothing to set here | off |
| Online sources | `ONLINE_SOURCES=1` in `.env`: Lampac's online modules and an "Online" button on each film | off |
| Turn off online sources | `LAMPAC_DISABLED_SOURCES=<Name>,<Name>` (names as Lampac's list shows them), for a source that fails on your network or player | none |
| Monitoring | a separate stack, [step 9](09-monitoring.md) | off |
| Indexers behind Cloudflare | `flaresolverr` profile, then the Prowlarr steps in [step 7](07-prowlarr-indexers.md) | off |
| VPN egress for the streaming engine | `vpn` profile and `TORRSERVER_EGRESS=vpn`, see below | off |


### VPN egress for the streaming engine

Zero upload (the default) means the server never sends file data to other peers, but it still connects to them,
so they see the public IP it connects from. The `vpn` profile routes that traffic through a VPN for privacy:
TorrServer runs inside a
[gluetun](https://github.com/qdm12/gluetun) container's network namespace and reaches the internet only
through a WireGuard tunnel. gluetun's firewall is the kill switch: when the tunnel is down TorrServer has no
internet at all, and gluetun reconnects by itself (about 30 s in our test). Search, the UI and every other
container keep their normal network.

1. Pick a VPN provider that allows peer-to-peer traffic and gives you a WireGuard configuration file (many call it a
   "router" or "manual" configuration). Turn off the provider's own DNS filtering options for this
   configuration: they may block domains the engine needs.
2. In `.env`, copy the fields of that file into the `WIREGUARD_*` variables. `Endpoint` must become
   `WIREGUARD_ENDPOINT_IP` as an IP address: resolve the hostname once (`nslookup <host>`). If the provider later
   moves that server, streaming stops (fail closed) until you update the IP.
3. Add `vpn` to `COMPOSE_PROFILES` and set `TORRSERVER_EGRESS=vpn`. In Portainer, set the same
   stack variables (**Load variables from .env file** takes a file), then redeploy.
4. Check: `docker compose logs vpn` shows `Public IP address is …` with the provider's address, and
   `docker compose exec torrserver wget -qO- https://ifconfig.me` prints the same address, not yours.

Lampa shows the state as a shield in its top bar: green when TorrServer's traffic leaves through the VPN
(select it to see the address and country peers see), red when the VPN or TorrServer is down, orange when no
VPN is configured. The gateway reads it from gluetun's control server; only its two read-only status routes are
open, and only inside the stack's network.

Use one WireGuard configuration on one machine at a time. If the `vpn` container is recreated on its own (an
image update), recreate TorrServer too: it keeps the old namespace and has no network until then; the
gateway's health check turns unhealthy within 10 minutes. A full stack redeploy does both.

The tunnel costs speed: on the reference Pi a well-connected test stream ran at 15–18 MB/s through the VPN against up
to 52–58 MB/s without it, still well above a 2160p remux (about 10 MB/s).
