# 8. Connect clients

Two kinds of client:

* **Browser and iPhone** load Lampa from the gateway. Nothing to install.
* **TVs and Android TV** use the stock Lampa app. You add one plugin URL once per device.

Menu names below are the English Lampa labels.

## Desktop browser

1. Open `http://<server>:<gateway-port>/h/<token>/` on the LAN, or the HTTPS tailnet address remotely
   ([step 2](02-tailscale.md)).
2. Bookmark it.

Some releases play without sound in browsers ([troubleshooting](../troubleshooting.md)).

## iPhone

1. Open the same address in Safari: `http://<server>:<gateway-port>/h/<token>/` at home. Away from home,
   with Tailscale on the phone, use the server's tailnet address instead of `<server>` (its `100.x` address or
   MagicDNS name, [step 2](02-tailscale.md)); everything else stays the same.
2. **Share → Add to Home Screen.** Lampa then opens like an app, full screen, from its own icon. Tested on an iPhone,
   at home and over Tailscale.

   The icon keeps the address it was made with. A LAN address (`192.168.x.x`) does not work away from home: the
   app shows a **white screen**, since a Home Screen app shows no error page. Either make the icon with the
   server's tailnet name (`http://<machine-name>:<gateway-port>/h/<token>/`, MagicDNS) and keep Tailscale on, on
   demand, on the phone (at home Tailscale then connects directly over the LAN), or let the server's Tailscale
   advertise the LAN as a subnet route (`--advertise-routes=192.168.x.0/24`, approved in the admin console) so
   the LAN address works remotely too. An exit node does not matter for this.
3. Optional: install an external player app that supports URL schemes, for example VLC, Infuse or nPlayer,
   and choose it in Lampa: **Settings → Player → Player type for torrents**.

Resume positions are not sent back from external players.

Media Station X (App Store, TV stores) is not a way in: its start parameter takes only a host and port and
loads `http://<host:port>/msx/start.json`, while everything here lives under the token path. On
an iPhone it would only open the same page as Safari.

## Android TV and Android boxes

Tested on: Chromecast with Google TV 4K. All 12 releases of the test set play, in the app's own player and in an
external one; resume, next episode (with a player from the list in step 4), audio and subtitle switching work
(matrix in [requirements](../requirements.md)).

1. Install the Lampa app for Android from its author's official channels (this project links none; upstream
   publishes no APK in its source repository). On Google TV, a sideloading app such as **Downloader** (Play
   Store) installs it once you allow it to install unknown apps.
2. On first start the app asks **"Укажите адрес Lampa"** (Lampa address) with `http://lampa.mx` prefilled.
   Replace it with `http://<server>:<gateway-port>/h/<token>/` and press **Сохранить** (Save). The token is
   `HOUSEHOLD_TOKEN` from your `.env` (or the stack's environment variables in Portainer). Typing 64
   characters with a remote is slow: on Google TV, the Google TV phone app's remote has a keyboard that
   accepts pasted text.
3. The plugin turns on **Settings → TorrServer → Built-in client** ("Встроенный клиент"); without it the
   Android app skips TorrServer and its player fails with "3003 Source error".
4. Install an external player and choose **Android** in **Settings → Player → Player type for torrents**;
   the system then asks which app to use (**Встроенный** is the app's own player). Since Lampa 3.4.7 any other
   choice plays in Lampa's own web player instead, after a warning that nothing is transcoded; on a TV box that
   player decodes fewer formats than a native one.
   For series, pick a player the app hands the whole season to, so the next episode starts by itself:
   **MX Player** (confirmed on Chromecast with Google TV 4K), ViMu, Just+ Player, UPlayer or DDD Player. The app
   gives **Just Player** and the others one file only and never starts the next one itself (lampa-app/LAMPA
   `MainActivity.kt`, `configureBroukenPlayerIntent`), so with them a series returns to the file list after each
   episode. Just Player stays a good choice for films: open source, uses the hardware decoders, decodes DTS and
   TrueHD in software. **VLC** as the fallback.

The device then runs the same pinned Lampa build as the browser, with CUB switched off and the project's
plugin loaded. How: the app's loader (`index/github/index.html:120-160` at the pinned commit) asks
`AndroidJS.getLampaURL()` and loads `lampainit.js`, `app.min.js` and `css/app.css` from that address; the
gateway's `lampainit.js` sets `lampa_settings` and loads the plugin. To change the address later, press and hold
**Back** (or press **Menu**) in the app and choose **Change Lampa URL** ("Изменить адрес Lampa").

If you keep another address (or your app build does not ask), add the plugin instead: **Settings →
Extensions → Add Plugin**, `http://<server>:<gateway-port>/h/<token>/plugins/modification.js`, then restart.

The Android app hands the resume position to the player and takes the result back
(`src/core/android.js:72-100, 143-149`); confirmed on the Chromecast with the app's own player and MX Player.

## Samsung (Tizen) and LG (webOS)

1. Install Lampa from the TV's own store (Samsung Apps, LG Content Store); community guides report it there
   since 2023. Untested here: there is no Samsung or LG TV in the household (unverified).
2. In Lampa: **Settings → Extensions → Add Plugin**, then enter
   `http://<server>:<gateway-port>/h/<token>/plugins/modification.js`.
3. Restart the app.

The TV apps always load the newest Lampa from upstream (`index/tizen/loader.js` and `index/webos/loader.js`
fetch `yumata.github.io/lampa/app.min.js`); see [troubleshooting](../troubleshooting.md) if a plugin stops
working. A plugin starts after Lampa, too late to switch CUB off: see the next section. TVs connect over the
LAN only.

### Optional: keep stock apps away from CUB

Stock Lampa apps load plugins from the CUB domains and send ad and metric requests there; the browser build
blocks this, the TV apps cannot. If your LAN has a DNS filter such as Pi-hole, deny
these domains **as wildcards** (they cover subdomains like `tmdb.` and `geo.`). The list is Lampa's built-in
mirror list (`src/core/manifest.js:55-81` at the pinned commit):

```
cub.best
cub.black
durex.monster
cubnotrip.top
kurwa-bober.ninja
nackhui.com
```

In Pi-hole: **Domains**, enter a domain, tick **Add domain as wildcard**, then **Add to denied domains**. Do
not block `yumata.github.io`: the TV apps load Lampa itself from there. The TVs must use the filter as their
DNS server (usually through the router's DHCP settings). Lampa keeps working without CUB.

## Profiles

Lampa opens with **"Who's watching?"** once the household has more than one profile, Netflix style: pick a
tile, or **Add** a profile (the remote's on-screen keyboard works) and **Edit** to rename or remove one. The
person icon in the top bar opens the same screen at any time. Profiles live on the server, so one made on the TV
appears on every device. Bookmarks, watch history and resume positions belong to the profile; switching
clears the device's local copy and restarts Lampa in the new profile, whose data comes back from the server.
The first profile is the shared one and cannot be removed. There are no PINs: profiles are a convenience, not a
lock. **Settings → Release search → Pick a profile at start** turns the start screen off.

## Search preferences

Each device has its own section **Settings → Release search** ("Поиск релизов"), added by the household
plugin. It decides which releases come first, not which are shown:

* **Audio language:** Russian (default), Ukrainian, or English / original.
* **Quality:** 4K, 1080p (default) or 720p.
* **Screen supports HDR:** turn it off for an SDR screen, and HDR and Dolby Vision releases move down.

The device class comes from Lampa itself; for example, Android devices push AV1 releases down, since the
Chromecast with Google TV 4K cannot decode AV1. The table is `devices` in `gateway/vocab.yaml`.

## Check

On each device, once:

1. The top bar shows the shield icon: green with the VPN on, orange without one.
2. A film's release list (**Torrents** in Lampa) shows results within a few seconds.
3. A release starts playing; for a series, the next episode starts by itself (Android: with a player from step 4).
4. Audio and subtitle tracks switch during playback (in the app's own player, or in the external player's menu).
5. Stop half-way, close the app, open it again: the card shows the progress and playback resumes there.
6. The same progress shows on a second device.
