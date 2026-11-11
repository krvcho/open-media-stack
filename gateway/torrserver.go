package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	streamBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_stream_bytes_total", Help: "Bytes proxied from TorrServer to clients on stream routes.",
	})
	tsRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_torrserver_rejected_total", Help: "Client requests to TorrServer refused by the gateway.",
	}, []string{"reason"})
)

// torrServer is the only client path to TorrServer. TorrServer's streaming routes are open
// even with auth on and accept arbitrary links, so the gateway allowlists routes and links.
type torrServer struct {
	base, user, pass string
	internalURL      string
	resolve          func(id string) (string, error)
	// ready is set once the enforced settings are live. Until then clients get 503: a torrent added
	// to a TorrServer still on upstream defaults would run with upload on.
	ready atomic.Bool
}

func isStreamPath(p string) bool {
	return p == "/stream" || strings.HasPrefix(p, "/stream/") || strings.HasPrefix(p, "/play/")
}

// tsAllowed lists the TorrServer routes Lampa needs; everything else (settings writes, shutdown, upload,
// MCP, WebDAV, gst, …) is refused.
func tsAllowed(method, p string) bool {
	get := method == http.MethodGet || method == http.MethodHead
	switch {
	case p == "/echo":
		return get
	case p == "/settings", p == "/torrents", p == "/cache", p == "/viewed":
		return method == http.MethodPost
	case isStreamPath(p), p == "/playlist", strings.HasPrefix(p, "/playlist/"):
		return get
	case strings.HasPrefix(p, "/ffp/"), strings.HasPrefix(p, "/subs/"):
		return get
	}
	return false
}

var (
	opaqueLink = regexp.MustCompile(`/h/[^/]+/t/([0-9a-f]{32})$`)
	infoHash   = regexp.MustCompile(`^(?i:[0-9a-f]{40}|[a-z2-7]{32})$`)
)

var errForeignLink = errors.New("link is not a gateway release, magnet or info hash")

// rewriteLink accepts our opaque release links (resolved server-side), magnets and bare info hashes.
// Any other URL is refused, so a client cannot make TorrServer fetch arbitrary URLs (SSRF).
func (t *torrServer) rewriteLink(link string) (string, error) {
	switch {
	case strings.HasPrefix(link, "magnet:"):
		return cleanMagnet(link)
	case infoHash.MatchString(link):
		return link, nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errForeignLink
	}
	m := opaqueLink.FindStringSubmatch(u.Path)
	if m == nil {
		return "", errForeignLink
	}
	return t.resolve(m[1])
}

// allowedActions are the POST actions Lampa sends per route (lampa-source/src/interaction/torserver.js).
var allowedActions = map[string]map[string]bool{
	"/settings": {"get": true},
	"/torrents": {"add": true, "get": true, "list": true, "drop": true, "rem": true},
	"/cache":    {"get": true},
	"/viewed":   {"set": true, "list": true, "rem": true},
}

// checkBody validates a POST body and rewrites the link of an "add".
func (t *torrServer) checkBody(p string, body []byte) ([]byte, string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "bad_json", err
	}
	action, _ := req["action"].(string)
	if !allowedActions[p][action] {
		return nil, "action", errors.New("action not allowed")
	}
	if link, ok := req["link"].(string); ok && link != "" {
		rewritten, err := t.rewriteLink(link)
		if err != nil {
			return nil, "link", err
		}
		req["link"] = rewritten
	}
	out, err := json.Marshal(req)
	return out, "", err
}

func (t *torrServer) handle(p string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !t.ready.Load() {
			http.Error(w, "torrserver settings not applied yet", http.StatusServiceUnavailable)
			return
		}
		if !tsAllowed(r.Method, p) {
			tsRejected.WithLabelValues("route").Inc()
			// The route only, never the query (it can carry links): what a client wanted that we do not serve.
			slog.Warn("torrserver route refused", "method", r.Method, "path", p)
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		if link := query.Get("link"); link != "" {
			rewritten, err := t.rewriteLink(link)
			if err != nil {
				tsRejected.WithLabelValues("link").Inc()
				rejectLink(w, err)
				return
			}
			query.Set("link", rewritten)
		}
		var body []byte
		if r.Method == http.MethodPost {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			var reason string
			if body, reason, err = t.checkBody(p, raw); err != nil {
				tsRejected.WithLabelValues(reason).Inc()
				if reason == "link" {
					rejectLink(w, err)
				} else {
					http.Error(w, "request not allowed", http.StatusBadRequest)
				}
				return
			}
		}
		target, _ := url.Parse(t.base)
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = p
				pr.Out.URL.RawPath = ""
				pr.Out.URL.RawQuery = query.Encode()
				pr.Out.Header.Del("Cookie")
				pr.Out.Header.Del("Authorization")
				pr.Out.SetBasicAuth(t.user, t.pass)
				if body != nil {
					pr.Out.Body = io.NopCloser(bytes.NewReader(body))
					pr.Out.ContentLength = int64(len(body))
					pr.Out.Header.Set("Content-Type", "application/json")
				}
			},
			FlushInterval: -1, // stream bytes as they arrive
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				// The client hung up, not TorrServer: Lampa's &preload request has a 2 s timeout (torrent.js
				// preload) while TorrServer holds it until the cache is warm. 499 as nginx counts it, no warning.
				if r.Context().Err() != nil {
					w.WriteHeader(499)
					return
				}
				// The default handler logs the upstream URL with its query; this one does not.
				slog.Warn("torrserver proxy", "err", safeErr(err))
				w.WriteHeader(http.StatusBadGateway)
			},
		}
		rp.ServeHTTP(w, r)
	}
}

// rejectLink answers a link that could not be used. It never echoes the error to the client: a failed
// Prowlarr download names upstream hosts.
func rejectLink(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUnknownLink):
		http.Error(w, "unknown or expired release link", http.StatusNotFound)
	case errors.Is(err, errForeignLink):
		http.Error(w, "link not allowed", http.StatusBadRequest)
	default:
		slog.Warn("release download failed", "err", err)
		http.Error(w, "release download failed", http.StatusBadGateway)
	}
}

var btih = regexp.MustCompile(`^urn:(btih:([0-9a-fA-F]{40}|[a-zA-Z2-7]{32})|btmh:[0-9a-fA-F]{68})$`)

// cleanMagnet rebuilds a client's magnet from its hash, name, size and public trackers only. TorrServer would
// otherwise fetch xs/as/ws URLs and announce to any tr URL from inside the stack's network, so a magnet could
// reach Prowlarr, the gateway or the LAN (server-side request forgery). Dropped: xs, as, ws, x.pe and every
// tracker that is not http(s)/udp on a public host.
func cleanMagnet(link string) (string, error) {
	q, err := url.ParseQuery(strings.TrimPrefix(strings.TrimPrefix(link, "magnet:"), "?"))
	if err != nil || !btih.MatchString(q.Get("xt")) {
		return "", errForeignLink
	}
	out := "magnet:?xt=" + q.Get("xt")
	if dn := q.Get("dn"); dn != "" {
		out += "&dn=" + url.QueryEscape(dn)
	}
	if xl := q.Get("xl"); xl != "" {
		if _, err := strconv.ParseUint(xl, 10, 64); err == nil {
			out += "&xl=" + xl
		}
	}
	for _, tr := range q["tr"] {
		if publicTracker(tr) {
			out += "&tr=" + url.QueryEscape(tr)
		}
	}
	return out, nil
}

// publicTracker: http, https or udp, on a dotted host name or a public IP. A name is not resolved here, so a
// public name that points inward still passes (limit: resolve and check if that ever matters; with the VPN
// on, the tunnel's firewall blocks the LAN anyway).
func publicTracker(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "udp") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
	}
	return strings.Contains(host, ".") && !strings.HasSuffix(host, ".localhost") && !strings.HasSuffix(host, ".local") &&
		!strings.HasSuffix(host, ".internal") && !strings.HasSuffix(host, ".lan") && !strings.HasSuffix(host, ".ts.net")
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10") // carrier-grade NAT, also Tailscale's addresses
