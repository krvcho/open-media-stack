// Command gateway is the only client-facing path to search, streaming and the Lampa web build.
// It keeps every upstream secret on the server.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type config struct {
	Listen         string // public listener (clients)
	InternalListen string // internal listener (metrics, TorrServer fetches); never published
	InternalURL    string // how TorrServer reaches the internal listener
	Token          string
	ProwlarrURL    string
	ProwlarrKey    string
	TorrServerURL  string
	TorrServerUser string
	TorrServerPass string
	LampaCommit    string
	LampaSHA256    string
	LampacPort     string
	LampacURL      string // how the gateway reaches Lampac inside the stack, for its probe
	LampacID       string // the household's Lampac account; every device sends it so sync lands in one place
	DataDir        string
	TSCacheMB      int    // TorrServer RAM cache the gateway enforces
	TSUploadKB     int    // TorrServer upload cap in KB/s; 0 = upload off
	MinSeeders     int    // releases with fewer seeders are dropped
	MaxResults     int    // results returned per search, best first
	Online         bool   // Lampac's online sources are loaded; the plugin then loads Lampac's online plugin
	TMDBKey        string // the household's TMDB key or read access token; empty = Lampa calls TMDB itself
	VPNControlURL  string // gluetun control server when TorrServer runs behind the VPN; empty = no VPN
}

// lampacID must be safe to put into a JS string and a URL path unescaped.
var lampacID = regexp.MustCompile(`^[a-z0-9]{16,64}$`)

func loadConfig() (config, error) {
	c := config{
		Listen:         env("GW_LISTEN", ":8080"),
		InternalListen: env("GW_INTERNAL_LISTEN", ":8081"),
		InternalURL:    env("GW_INTERNAL_URL", "http://gateway:8081"),
		Token:          os.Getenv("GW_TOKEN"),
		ProwlarrURL:    strings.TrimRight(env("GW_PROWLARR_URL", "http://prowlarr:9696"), "/"),
		ProwlarrKey:    os.Getenv("GW_PROWLARR_APIKEY"),
		TorrServerURL:  strings.TrimRight(env("GW_TORRSERVER_URL", "http://torrserver:8090"), "/"),
		TorrServerUser: os.Getenv("GW_TORRSERVER_USER"),
		TorrServerPass: os.Getenv("GW_TORRSERVER_PASSWORD"),
		LampaCommit:    os.Getenv("GW_LAMPA_COMMIT"),
		LampaSHA256:    os.Getenv("GW_LAMPA_SHA256"),
		LampacPort:     env("GW_LAMPAC_PORT", "9118"),
		LampacURL:      strings.TrimRight(env("GW_LAMPAC_URL", "http://lampac:9118"), "/"),
		LampacID:       os.Getenv("GW_LAMPAC_ID"),
		DataDir:        env("GW_DATA_DIR", "/data"),
		VPNControlURL:  strings.TrimRight(os.Getenv("GW_VPN_CONTROL_URL"), "/"),
		TMDBKey:        strings.TrimSpace(os.Getenv("GW_TMDB_KEY")),
		Online:         strings.TrimSpace(os.Getenv("GW_ONLINE_SOURCES")) != "",
	}
	var missing []string
	for name, v := range map[string]string{
		"GW_TOKEN": c.Token, "GW_PROWLARR_APIKEY": c.ProwlarrKey,
		"GW_TORRSERVER_USER": c.TorrServerUser, "GW_TORRSERVER_PASSWORD": c.TorrServerPass,
		"GW_LAMPA_COMMIT": c.LampaCommit, "GW_LAMPA_SHA256": c.LampaSHA256, "GW_LAMPAC_ID": c.LampacID,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}
	if len(c.Token) < 32 {
		return c, errors.New("GW_TOKEN must be at least 32 characters")
	}
	if !lampacID.MatchString(c.LampacID) {
		return c, errors.New("GW_LAMPAC_ID must be 16-64 lowercase letters or digits")
	}
	if c.LampacID == c.Token {
		return c, errors.New("GW_LAMPAC_ID must differ from GW_TOKEN")
	}
	var err error
	if c.TSCacheMB, err = strconv.Atoi(env("GW_TS_CACHE_MB", "512")); err != nil || c.TSCacheMB < 32 {
		return c, errors.New("GW_TS_CACHE_MB must be a whole number of MB, at least 32")
	}
	if c.TSUploadKB, err = strconv.Atoi(env("GW_TS_UPLOAD_KB", "0")); err != nil || c.TSUploadKB < 0 {
		return c, errors.New("GW_TS_UPLOAD_KB must be 0 (upload off) or a positive KB/s cap")
	}
	if c.MinSeeders, err = strconv.Atoi(env("GW_MIN_SEEDERS", "3")); err != nil || c.MinSeeders < 0 {
		return c, errors.New("GW_MIN_SEEDERS must be a whole number, 0 or more")
	}
	if c.MaxResults, err = strconv.Atoi(env("GW_MAX_RESULTS", "50")); err != nil || c.MaxResults < 1 {
		return c, errors.New("GW_MAX_RESULTS must be a whole number, at least 1")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// safeErr drops the URL from a *url.Error. Prowlarr download URLs carry its API key, and search URLs carry
// what the household searched for; neither may reach a client or a log.
func safeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_http_requests_total", Help: "Client requests by route and status code.",
	}, []string{"route", "code"})
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gateway_http_request_duration_seconds", Help: "Client request duration by route.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"route"})
	authRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_auth_rejected_total", Help: "Requests with a missing or wrong household token.",
	})
	streamTTFB = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "gateway_stream_ttfb_seconds", Help: "Time from a player's stream request to its first byte.",
		Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 7.5, 10, 15, 30, 60},
	})
	streamsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gateway_streams_active", Help: "Player streams being served now.",
	})
)

// recorder captures status and size; Unwrap keeps Flush working for streaming through ResponseController.
type recorder struct {
	http.ResponseWriter
	status  int
	bytes   int64
	counter prometheus.Counter // when set, counts bytes as they are written, so rate() is live during long streams
	first   func()             // when set, called once before the first byte is written
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.first != nil {
		r.first()
		r.first = nil
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	if r.counter != nil {
		r.counter.Add(float64(n))
	}
	return n, err
}
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// observe wraps a handler with metrics and one structured log line. It never logs URLs or bodies,
// because query strings can carry tokens and links.
func observe(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		if route == "ts_stream" {
			rec.counter = streamBytes
			// Lampa's player asks with &play (&preload when it only warms the cache); TTFB is the player's wait.
			if r.URL.Query().Has("play") {
				streamsActive.Inc()
				defer streamsActive.Dec()
				rec.first = func() { streamTTFB.Observe(time.Since(start).Seconds()) }
			}
		}
		h(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		d := time.Since(start)
		httpRequests.WithLabelValues(route, fmt.Sprint(rec.status)).Inc()
		httpDuration.WithLabelValues(route).Observe(d.Seconds())
		slog.Info("request", "route", route, "method", r.Method, "status", rec.status,
			"bytes", rec.bytes, "duration_ms", d.Milliseconds())
	}
}

type gateway struct {
	cfg   config
	lampa string // directory of the extracted Lampa build
	links *linkStore
	ts    *torrServer
	pr    *prowlarr
	fan   *fanout
	tmdb  *tmdbProxy // nil without a TMDB key
	prof  *profileStore
}

// publicHandler routes /h/{token}/... Wrong tokens get 404, not 401.
func (g *gateway) publicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !g.ts.ready.Load() { // not ready until TorrServer runs the enforced settings
			http.Error(w, "torrserver settings not applied yet", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/h/{token}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(g.cfg.Token)) != 1 {
			authRejected.Inc()
			http.NotFound(w, r)
			return
		}
		rest := r.PathValue("rest")
		switch {
		case strings.HasPrefix(rest, "jackett/api/v2.0/indexers/") || isPrefsSearch(rest):
			observe("search", g.handleSearch)(w, r)
		case strings.HasPrefix(rest, "ts/"):
			route := "ts_api"
			if isStreamPath(strings.TrimPrefix(rest, "ts")) {
				route = "ts_stream"
			}
			observe(route, g.ts.handle(strings.TrimPrefix(rest, "ts")))(w, r)
		case strings.HasPrefix(rest, "t/"):
			observe("torrent", g.handlePublicTorrent(strings.TrimPrefix(rest, "t/")))(w, r)
		case rest == "plugins/modification.js":
			observe("modification", g.handleModification)(w, r)
		case rest == "profiles":
			observe("profiles", g.prof.handle)(w, r)
		case rest == "plugins/platforms.js" || rest == "platforms.json" || rest == "plugins/profiles.js":
			observe("platforms", servePlatforms(rest))(w, r)
		case rest == "status":
			observe("status", g.handleStatus)(w, r)
		case rest == "beacon":
			observe("beacon", handleBeacon)(w, r)
		case rest == "lampainit.js" || rest == "lampa-main/lampainit.js":
			observe("lampainit", g.handleLampainit)(w, r)
		default:
			observe("lampa", g.handleLampa(rest))(w, r)
		}
	})
	// Lampa puts the whole TMDB URL into the path (…/tmdb/https://api.themoviedb.org/3/…). ServeMux would answer
	// its "//" with a redirect, one more round trip per request, so this route is matched before the mux.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/h/"), "/"); ok && g.tmdb != nil &&
			strings.HasPrefix(r.URL.Path, "/h/") && strings.HasPrefix(rest, "tmdb/") {
			if subtle.ConstantTimeCompare([]byte(token), []byte(g.cfg.Token)) != 1 {
				authRejected.Inc()
				http.NotFound(w, r)
				return
			}
			observe("tmdb", g.tmdb.handle(strings.TrimPrefix(rest, "tmdb/")))(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (g *gateway) internalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /internal/t/{id}", observe("internal_torrent", g.handleInternalTorrent))
	return mux
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := http.Get("http://127.0.0.1" + env("GW_LISTEN", ":8080") + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(2)
	}
	dir, err := ensureLampa(cfg.DataDir, cfg.LampaCommit, cfg.LampaSHA256)
	if err != nil {
		slog.Error("lampa build", "err", err)
		os.Exit(1)
	}
	g := &gateway{
		cfg:   cfg,
		lampa: dir,
		links: newLinkStore(24*time.Hour, 50000),
		ts:    &torrServer{base: cfg.TorrServerURL, user: cfg.TorrServerUser, pass: cfg.TorrServerPass, internalURL: cfg.InternalURL},
		pr:    newProwlarr(cfg.ProwlarrURL, cfg.ProwlarrKey),
	}
	g.ts.resolve = g.resolveLink
	g.fan = newFanout(g.pr)
	g.prof = newProfileStore(cfg.DataDir)
	prometheus.MustRegister(newProbeCollector(g))
	if cfg.TMDBKey != "" {
		g.tmdb = newTMDB(cfg.TMDBKey)
	}

	public := &http.Server{Addr: cfg.Listen, Handler: g.publicHandler(), ReadHeaderTimeout: 10 * time.Second}
	internal := &http.Server{Addr: cfg.InternalListen, Handler: g.internalHandler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go g.ts.keepSettings(ctx, wantSettings(cfg.TSCacheMB, cfg.TSUploadKB))
	for _, s := range []*http.Server{public, internal} {
		go func(s *http.Server) {
			slog.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("listen", "addr", s.Addr, "err", err)
				stop()
			}
		}(s)
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdown)
	_ = internal.Shutdown(shutdown)
}

// isPrefsSearch matches the search URL with the viewer's preferences: jackett/p/<prefs>/api/v2.0/indexers/…
func isPrefsSearch(rest string) bool {
	after, ok := strings.CutPrefix(rest, "jackett/p/")
	if !ok {
		return false
	}
	_, path, _ := strings.Cut(after, "/")
	return strings.HasPrefix(path, "api/v2.0/indexers/")
}
