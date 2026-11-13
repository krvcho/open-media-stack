package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxArchiveBytes = 100 << 20 // the pinned build is about 10 MB
	maxExtractBytes = 400 << 20
)

// GitHub's archive endpoint can hang without answering. A failed download exits the process, and the
// container's restart policy retries it.
var lampaClient = &http.Client{Timeout: 5 * time.Minute}

// ensureLampa returns the directory of the pinned Lampa web build, downloading and verifying it on first
// start. A checksum mismatch is fatal: we never serve an unverified build.
func ensureLampa(dataDir, commit, wantSHA256 string) (string, error) {
	dir := filepath.Join(dataDir, "lampa", commit)
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		return dir, nil
	}
	url := "https://github.com/yumata/lampa/archive/" + commit + ".tar.gz"
	slog.Info("downloading lampa build", "commit", commit)
	resp, err := lampaClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if len(archive) > maxArchiveBytes {
		return "", errors.New("lampa archive larger than expected")
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, wantSHA256) {
		return "", fmt.Errorf("lampa archive checksum mismatch: got %s, want %s", got, wantSHA256)
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractTarGz(bytes.NewReader(archive), tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// extractTarGz unpacks a GitHub archive into dst, dropping the top-level "<repo>-<sha>/" directory.
// Only regular files and directories are written; any path escaping dst is an error.
func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("unsafe path in archive: %q", h.Name)
		}
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		} else {
			continue // the top-level directory itself
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("unsafe path in archive: %q", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxExtractBytes {
				return errors.New("lampa archive expands beyond the size limit")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, h.Size))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			// symlinks, devices and the like are skipped on purpose
		}
	}
}

// lampaSettings switches off CUB-dependent features before app.min.js boots (lampa-source src/app.js:136-200).
// Each key here removes a request to the CUB domains; a few have no setting and are blocked by the CSP below.
var lampaSettings = map[string]any{
	"account_use":   false,
	"account_sync":  false,
	"socket_use":    false,
	"plugins_store": false,
	"services":      false, // cub plugins "sport" and "tsarea" (src/services/libs.js:23-26)
	"geo":           false, // geo.<cub> region lookup (src/core/vpn.js:16-17)
	"disable_features": map[string]bool{
		"dmca": true, "reactions": true, "discuss": true, "ai": true, "subscribe": true,
		"blacklist": true, "persons": true, "ads": true, "install_proxy": true, "remote_configuration": true,
		"metadata": true, // cub /api/ai/video-view metadata (src/core/api/sources/cub.js:577)
		"lgbt":     true, // remote filter list from tmdb.<cub> (src/services/lgbt.js:12)
		"friends":  true, // CUB friends and "watching now" (Lampa 3.4.7, src/core/account/friends.js:33)
	},
}

// contentSecurityPolicy keeps the browser build talking only to the gateway, Lampac and TMDB. Lampa loads
// plugins from the CUB domains (shots, engineer, seasonal events: src/services/libs.js:29-32,
// src/services/events.js:69) and sends ad, mirror-check and metric requests there, with no setting to stop
// them; the browser now refuses them. Images are not restricted (posters come from image.tmdb.org).
func contentSecurityPolicy(lampac string, tmdbProxied bool) string {
	ws := "ws" + strings.TrimPrefix(lampac, "http") // http→ws, https→wss for Lampac's sync events socket
	tmdb := " https://api.themoviedb.org"
	if tmdbProxied {
		tmdb = "" // the gateway proxies TMDB's API; the browser has no reason to call it
	}
	return "script-src 'self' 'unsafe-inline' 'unsafe-eval' " + lampac +
		"; connect-src 'self' " + lampac + " " + ws + tmdb +
		"; media-src 'self' blob: " + lampac
}

// injectSettings puts a lampa_settings script right after <head>, so it runs before Lampa's loader.
func injectSettings(index []byte) ([]byte, error) {
	i := bytes.Index(index, []byte("<head>"))
	if i < 0 {
		return nil, errors.New("lampa index.html has no <head>; refusing to serve it unmodified")
	}
	js, err := json.Marshal(lampaSettings)
	if err != nil {
		return nil, err
	}
	tag := []byte("\n<script>window.lampa_settings=" + string(js) + ";</script>")
	at := i + len("<head>")
	return append(append(append([]byte{}, index[:at]...), tag...), index[at:]...), nil
}

var (
	indexOnce sync.Once
	indexHTML []byte
	indexErr  error
)

func (g *gateway) handleLampa(rest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if rest == "" || rest == "index.html" {
			indexOnce.Do(func() {
				var b []byte
				if b, indexErr = os.ReadFile(filepath.Join(g.lampa, "index.html")); indexErr == nil {
					indexHTML, indexErr = injectSettings(b)
				}
			})
			if indexErr != nil {
				slog.Error("index.html", "err", indexErr)
				http.Error(w, "lampa build unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", contentSecurityPolicy(lampacBase(r, g.cfg.LampacPort), g.tmdb != nil))
			_, _ = w.Write(indexHTML)
			return
		}
		if !fs.ValidPath(rest) {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, os.DirFS(g.lampa), rest)
	}
}

// clientBase is the absolute URL of this household's gateway root as the client sees it.
func clientBase(r *http.Request, token string) string {
	return scheme(r) + "://" + r.Host + "/h/" + token
}

func scheme(r *http.Request) string {
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" {
		return "https"
	}
	return "http"
}

// lampacBase is Lampac on the same host as the gateway, on its own port.
func lampacBase(r *http.Request, port string) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.Contains(host, ":") { // bare IPv6 literal
		host = "[" + host + "]"
	}
	return scheme(r) + "://" + host + ":" + port
}

// modificationJS is loaded by Lampa from ./plugins/modification.js. It must stay ES5.
const modificationJS = `(function () {
  'use strict';
  if (window.media_stack_ready) return;
  window.media_stack_ready = true;

  var base = %s;
  var lampac = %s;
  var household = %s;
  var tmdbProxy = %s;
  var online = %s;

  function set(name, value) {
    if (Lampa.Storage.get(name) !== value) Lampa.Storage.set(name, value);
  }

  // Release search goes to the gateway's Jackett-compatible endpoint; no key on the client.
  set('parser_use', true);
  set('parser_torrent_type', 'jackett');
  // The viewer's preferences and device class ride in the search URL's path, because
  // Lampa builds the query itself. They live in Lampa's settings on this device.
  function searchURL() {
    var lang = Lampa.Storage.field('ms_lang') || 'ru';
    var res = Lampa.Storage.field('ms_res') || '1080';
    var hdr = Lampa.Storage.field('ms_hdr') === false ? 0 : 1;
    var device = Lampa.Platform && Lampa.Platform.get ? Lampa.Platform.get() : '';
    set('jackett_url', base + '/jackett/p/l=' + lang + ',r=' + res + ',h=' + hdr + ',d=' + device);
  }
  if (Lampa.SettingsApi && Lampa.Lang) {
    Lampa.Lang.add({
      media_stack_prefs: {ru: 'Поиск релизов', uk: 'Пошук релізів', en: 'Release search'},
      media_stack_lang: {ru: 'Язык озвучки', uk: 'Мова озвучення', en: 'Audio language'},
      media_stack_lang_descr: {ru: 'Релизы на этом языке идут первыми', uk: 'Релізи цією мовою йдуть першими',
        en: 'Releases in this language come first'},
      media_stack_res: {ru: 'Качество', uk: 'Якість', en: 'Quality'},
      media_stack_res_descr: {ru: 'Релизы в этом разрешении идут первыми',
        uk: 'Релізи в цій роздільності йдуть першими', en: 'Releases in this resolution come first'},
      media_stack_hdr: {ru: 'Экран поддерживает HDR', uk: 'Екран підтримує HDR', en: 'Screen supports HDR'},
      media_stack_hdr_descr: {ru: 'Выключите для обычного (SDR) экрана: HDR и Dolby Vision уйдут вниз',
        uk: 'Вимкніть для звичайного (SDR) екрана: HDR і Dolby Vision підуть донизу',
        en: 'Turn off for an SDR screen: HDR and Dolby Vision move down'}
    });
    Lampa.SettingsApi.addComponent({component: 'media_stack', name: Lampa.Lang.translate('media_stack_prefs'),
      icon: '<svg viewBox="0 0 24 24" width="24" height="24"><path fill="currentColor" d="M10 2a8 8 0 0 1 6.32 ' +
        '12.9l5.39 5.4-1.41 1.4-5.4-5.39A8 8 0 1 1 10 2zm0 2a6 6 0 1 0 0 12 6 6 0 0 0 0-12z"/></svg>'});
    var params = [
      {name: 'ms_lang', type: 'select', values: {ru: 'Русский', uk: 'Українська', en: 'English / Original'},
        'default': 'ru', key: 'media_stack_lang'},
      {name: 'ms_res', type: 'select', values: {'2160': '4K (2160p)', '1080': '1080p', '720': '720p'},
        'default': '1080', key: 'media_stack_res'},
      {name: 'ms_hdr', type: 'trigger', 'default': true, key: 'media_stack_hdr'}
    ];
    for (var i = 0; i < params.length; i++) {
      Lampa.SettingsApi.addParam({component: 'media_stack',
        param: {name: params[i].name, type: params[i].type, values: params[i].values, 'default': params[i]['default']},
        field: {name: Lampa.Lang.translate(params[i].key), description: Lampa.Lang.translate(params[i].key + '_descr')},
        onChange: searchURL});
    }
  }
  searchURL();

  set('jackett_key', '');
  // The gateway answers a search within 25 s at most (fanout.go hardCap); Lampa's default 15 s can be shorter.
  if (parseInt(Lampa.Storage.get('parse_timeout', '15'), 10) < 30) set('parse_timeout', '30');
  // Lampa sorts torrent results by the field named in "torrents_sort"; the gateway's order is in "Rank".
  // Set it once only, so a sort the user later picks in Lampa's menu stays.
  if (!Lampa.Storage.get('media_stack_sort_set', '')) {
    Lampa.Storage.set('torrents_sort', 'Rank');
    Lampa.Storage.set('media_stack_sort_set', '1');
  }
  // TMDB's API through the gateway, with the household's key and cache. Without a key on the
  // server, undo our proxy setting so Lampa calls TMDB itself again.
  if (tmdbProxy) {
    set('proxy_tmdb', true);
    set('tmdb_proxy_api', base + '/tmdb');
  } else if (String(Lampa.Storage.get('tmdb_proxy_api', '')).indexOf(base) === 0) {
    set('tmdb_proxy_api', '');
  }
  // TorrServer only through the gateway.
  set('torrserver_url', base + '/ts');
  set('torrserver_use_link', 'one');
  // The Android app defaults this to false (lampa-source settings/params.js:84) and then hands the release
  // link to the system instead of TorrServer (interaction/torrent.js:64); the player got the .torrent file.
  set('internal_torrclient', true);
  // Playback beacon: time to first frame, stalls and fatal errors in Lampa's own player (browser,
  // Tizen, webOS). External players report nothing back, and Lampa sends no 'start' for them.
  if (Lampa.Player && Lampa.Player.listener && Lampa.PlayerVideo && Lampa.PlayerVideo.listener) {
    var play = null;
    var beacon = function (event, seconds) {
      if (!play) return;
      $.ajax({url: base + '/beacon', type: 'POST', contentType: 'application/json', timeout: 8000,
        data: JSON.stringify({e: event, s: seconds || 0, p: Lampa.Platform.get(), src: play.source})});
    };
    Lampa.Player.listener.follow('start', function (data) {
      if (data.iptv) return;
      play = {t0: Date.now(), first: false, seekAt: 0,
        source: data.torrent_hash ? 'torrent' : (data.isonline ? 'online' : 'other')};
    });
    Lampa.Player.listener.follow('ready', function () {
      var video = Lampa.PlayerVideo.video();
      if (!play || !video || video.media_stack_beacon) return;
      video.media_stack_beacon = true;
      video.addEventListener('playing', function () {
        if (play && !play.first) {
          play.first = true;
          beacon('ttff', (Date.now() - play.t0) / 1000);
        }
      });
      video.addEventListener('seeking', function () { if (play) play.seekAt = Date.now(); });
      // A stall after the first frame that no seek in the last 2 s explains.
      video.addEventListener('waiting', function () {
        if (play && play.first && !video.seeking && Date.now() - play.seekAt > 2000) beacon('rebuffer');
      });
    });
    Lampa.PlayerVideo.listener.follow('error', function (e) { if (e && e.fatal) beacon('error'); });
    Lampa.Player.listener.follow('destroy', function () { play = null; });
  }
  // Lampac's token routes put the household id into every sync request, and Lampac resolves the user
  // from "token" first, so every device lands in the same data.
  // invc-ws is Lampac's WebSocket client: it pushes another device's bookmark and timecode changes live
  // (the 'lwsEvent' the two sync plugins listen for); without it they only sync at start.
  // It asks Lampac's /reqinfo for the user id first, which lives in the LampaWeb module we do not load;
  // it skips the request when window.reqinfo is set. Lampac addresses sync events to the account the token
  // names (accsdb: the id is the household id), so that is the user id here.
  window.reqinfo = window.reqinfo || {user_uid: household};
  var syncStarted = false;
  function loadSync() {
    if (syncStarted) return;
    syncStarted = true;
    Lampa.Utils.putScriptAsync([
      lampac + '/invc-ws/js/' + household,
      lampac + '/timecode/js/' + household,
      lampac + '/bookmark/js/' + household
    ], function () {});
  }
  window.media_stack = {base: base, loadSync: loadSync};
  // Profiles: the "Who's watching?" screen picks the profile, then starts sync under it. If the
  // picker cannot load, sync starts anyway under the device's current profile.
  Lampa.Utils.putScriptAsync([base + '/plugins/profiles.js'], function () {}, function () { loadSync(); });
  // Online sources: Lampac's own online plugin, only when the server has them on.
  if (online) Lampa.Utils.putScriptAsync([lampac + '/online/js/' + household], function () {});
  // The Platforms screen, a separate file so it can fail on its own.
  Lampa.Utils.putScriptAsync([base + '/plugins/platforms.js'], function () {});

  // Top-bar indicator: green = TorrServer's traffic leaves through the VPN, red = the VPN or
  // TorrServer is down, orange = no VPN configured. Selecting it shows the address the swarm sees.
  // Plugins load before Lampa builds its top bar (lampa-source src/app.js:800 vs 527), so wait for "ready".
  if (window.appready) indicator();
  else Lampa.Listener.follow('app', function (e) { if (e.type === 'ready') indicator(); });

  function indicator() {
    if (!Lampa.Head || !Lampa.Head.addIcon || !Lampa.Lang) return;
    Lampa.Lang.add({
      media_stack_vpn_on: {ru: 'VPN работает', uk: 'VPN працює', en: 'VPN is on'},
      media_stack_vpn_down: {ru: 'VPN не работает: стриминг остановлен', uk: 'VPN не працює: стримінг зупинено',
        en: 'VPN is down: streaming is stopped'},
      media_stack_vpn_none: {ru: 'VPN не настроен: стриминг идёт с домашнего адреса',
        uk: 'VPN не налаштовано: стримінг іде з домашньої адреси', en: 'No VPN: streaming uses your home address'},
      media_stack_ts_down: {ru: 'TorrServer недоступен', uk: 'TorrServer недоступний', en: 'TorrServer is unreachable'}
    });
    var text = '';
    var icon = Lampa.Head.addIcon('<svg viewBox="0 0 24 24" width="24" height="24"><path fill="currentColor" ' +
      'd="M12 1 3 5v6c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V5l-9-4zm-2 16-4-4 1.41-1.41L10 14.17l6.59-6.59' +
      'L18 9l-8 8z"/></svg>', function () { Lampa.Noty.show(text); });
    // Lampa binds the mouse to top-bar items only when the remote first visits the bar; until then, ours.
    icon[0].addEventListener('click', function () { if (!icon[0].bind_events) Lampa.Noty.show(text); });
    function show(color, message) {
      text = message;
      icon.css('color', color).attr('title', message);
    }
    function check() {
      $.ajax({url: base + '/status', dataType: 'json', timeout: 8000,
        success: function (s) {
          if (!s.torrserver) return show('#f44336', Lampa.Lang.translate('media_stack_ts_down'));
          if (!s.vpn) return show('#ff9800', Lampa.Lang.translate('media_stack_vpn_none'));
          if (!s.vpn.running) return show('#f44336', Lampa.Lang.translate('media_stack_vpn_down'));
          show('#4caf50', Lampa.Lang.translate('media_stack_vpn_on') + ': ' + s.vpn.ip +
            (s.vpn.country ? ' (' + s.vpn.country + ')' : ''));
        },
        error: function () { show('#f44336', Lampa.Lang.translate('media_stack_ts_down')); }
      });
    }
    check();
    setInterval(check, 60000);
  }
})();
`

// lampainitJS is for the Lampa Android app with its Lampa URL set to the household root. The app's bundled
// loader (lampa-source index/github/index.html:120-160) then loads lampainit.js (a failure is ignored) and
// app.min.js from that URL. The app's page is local, so neither
// our injected lampa_settings nor ./plugins/modification.js reach it: this script brings both. Both scripts
// load async; lampa_settings only takes effect if this one runs first (it is tiny and usually does), the
// plugin is loaded once Lampa exists either way. ES5.
const lampainitJS = `(function () {
  'use strict';
  window.lampa_settings = %s;
  var tries = 0;
  (function wait() {
    if (window.Lampa && Lampa.Storage && Lampa.Utils) {
      var s = document.createElement('script');
      s.src = %s;
      document.head.appendChild(s);
    } else if (tries++ < 600) {
      setTimeout(wait, 100);
    }
  })();
})();
`

func (g *gateway) handleLampainit(w http.ResponseWriter, r *http.Request) {
	settings, _ := json.Marshal(lampaSettings)
	src, _ := json.Marshal(clientBase(r, g.cfg.Token) + "/plugins/modification.js")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, lampainitJS, settings, src)
}

func (g *gateway) handleModification(w http.ResponseWriter, r *http.Request) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, modificationJS, q(clientBase(r, g.cfg.Token)), q(lampacBase(r, g.cfg.LampacPort)), q(g.cfg.LampacID),
		strconv.FormatBool(g.tmdb != nil), strconv.FormatBool(g.cfg.Online))
}

//go:embed platforms.js
var platformsJS []byte

//go:embed platforms.json
var platformsJSON []byte

//go:embed profiles.js
var profilesJS []byte

// servePlatforms serves the Platforms plugin and its list of services. The list is data: a name and
// a pattern for TMDB's provider name; the ids come from TMDB for the viewer's region.
func servePlatforms(rest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*") // stock TV apps call from their own origin
		if rest == "platforms.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(platformsJSON)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		if rest == "plugins/profiles.js" {
			_, _ = w.Write(profilesJS)
			return
		}
		_, _ = w.Write(platformsJS)
	}
}
