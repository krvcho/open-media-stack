package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// TMDB proxy. Lampa's own proxy setting points at the gateway, and Lampa then puts the whole TMDB
// URL into the path: …/tmdb/https://api.themoviedb.org/3/movie/603?language=ru&api_key=<Lampa's key>. The
// gateway accepts only api.themoviedb.org/3, drops the client's key, adds the household's, and caches the
// answers in memory, so one TMDB call serves every device. Images still load straight from image.tmdb.org.

var (
	tmdbRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_tmdb_requests_total", Help: "TMDB proxy requests by result: hit, miss, stale, error.",
	}, []string{"result"})
	tmdbUpstream = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "gateway_tmdb_upstream_seconds", Help: "Time TMDB took to answer the gateway.",
		Buckets: []float64{.05, .1, .2, .3, .5, .8, 1.2, 2, 5},
	})
	tmdbThrottled = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_tmdb_throttled_total", Help: "HTTP 429 answers from TMDB.",
	})
)

type tmdbEntry struct {
	body    []byte
	expires time.Time
}

type tmdbProxy struct {
	key      string // API key (v3) or read access token (v4, a JWT)
	upstream string // https://api.themoviedb.org/3
	client   *http.Client
	max      int

	mu      sync.Mutex
	cache   map[string]*tmdbEntry
	blocked time.Time // after a 429, until TMDB's Retry-After
}

func newTMDB(key string) *tmdbProxy {
	return &tmdbProxy{key: key, upstream: "https://api.themoviedb.org/3", client: &http.Client{Timeout: 10 * time.Second},
		max: 5000, cache: map[string]*tmdbEntry{}}
}

var (
	tmdbTarget = regexp.MustCompile(`^https?:/{1,2}api\.themoviedb\.org/3/`)
	tmdbDetail = regexp.MustCompile(`^(movie|tv|person|collection)/\d+(/|$)`)
	tmdbRoot   = regexp.MustCompile(`^(movie|tv)/\d+$`)
)

// tmdbTTL: configuration and genres change rarely, details daily, lists (trending, discover, search) hourly.
func tmdbTTL(path string) time.Duration {
	switch {
	case strings.HasPrefix(path, "configuration"), strings.HasPrefix(path, "genre/"),
		strings.HasPrefix(path, "watch/providers/"):
		return 7 * 24 * time.Hour
	case tmdbDetail.MatchString(path):
		return 24 * time.Hour
	}
	return time.Hour
}

func (t *tmdbProxy) handle(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loc := tmdbTarget.FindStringIndex(target)
		if loc == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r) // only api.themoviedb.org/3, read-only: never an open proxy
			return
		}
		path := target[loc[1]:]
		q := r.URL.Query()
		q.Del("api_key") // Lampa's built-in key; the household's is added upstream
		q.Del("email")
		body, status := t.get(r.Context(), path, q)
		if status == http.StatusOK && tmdbRoot.MatchString(path) {
			body = t.fillLanguage(r.Context(), path, q.Get("language"), body)
		}
		w.Header().Set("Access-Control-Allow-Origin", "*") // stock TV apps call from their own origin
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		if status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "10")
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

// get answers from the cache or TMDB. A stale answer beats none when TMDB fails or throttles.
func (t *tmdbProxy) get(ctx context.Context, path string, q url.Values) ([]byte, int) {
	key := path + "?" + q.Encode() // Encode sorts the parameters
	now := time.Now()
	t.mu.Lock()
	e := t.cache[key]
	blocked := now.Before(t.blocked)
	t.mu.Unlock()
	if e != nil && now.Before(e.expires) {
		tmdbRequests.WithLabelValues("hit").Inc()
		return e.body, http.StatusOK
	}
	stale := func(status int) ([]byte, int) {
		if e != nil {
			tmdbRequests.WithLabelValues("stale").Inc()
			return e.body, http.StatusOK
		}
		tmdbRequests.WithLabelValues("error").Inc()
		return []byte(`{"status_message":"TMDB unavailable"}`), status
	}
	if blocked {
		return stale(http.StatusServiceUnavailable)
	}
	body, status, retry, err := t.fetch(ctx, path, q)
	switch {
	case err != nil:
		slog.Warn("tmdb", "path", path, "err", safeErr(err))
		return stale(http.StatusBadGateway)
	case status == http.StatusTooManyRequests:
		tmdbThrottled.Inc()
		t.mu.Lock()
		t.blocked = time.Now().Add(retry)
		t.mu.Unlock()
		slog.Warn("tmdb throttled", "retry_after", retry)
		return stale(http.StatusServiceUnavailable)
	case status != http.StatusOK:
		tmdbRequests.WithLabelValues("error").Inc()
		return body, status // 404 and the like: TMDB's own answer
	}
	tmdbRequests.WithLabelValues("miss").Inc()
	t.store(key, body, tmdbTTL(path))
	return body, http.StatusOK
}

func (t *tmdbProxy) fetch(ctx context.Context, path string, q url.Values) ([]byte, int, time.Duration, error) {
	up := url.Values{}
	for k, v := range q {
		up[k] = v
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "", nil)
	if err != nil {
		return nil, 0, 0, err
	}
	if strings.HasPrefix(t.key, "eyJ") { // a v4 read access token
		req.Header.Set("Authorization", "Bearer "+t.key)
	} else {
		up.Set("api_key", t.key)
	}
	if req.URL, err = url.Parse(t.upstream + "/" + path + "?" + up.Encode()); err != nil {
		return nil, 0, 0, err
	}
	start := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	tmdbUpstream.Observe(time.Since(start).Seconds())
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	retry := 10 * time.Second
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s < 600 {
		retry = time.Duration(s) * time.Second
	}
	return body, resp.StatusCode, retry, err
}

func (t *tmdbProxy) store(key string, body []byte, ttl time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.cache) >= t.max { // expired first, then whatever map order gives: a cache, not an archive
		now := time.Now()
		for k, e := range t.cache {
			if now.After(e.expires) {
				delete(t.cache, k)
			}
		}
		for k := range t.cache {
			if len(t.cache) < t.max*9/10 {
				break
			}
			delete(t.cache, k)
		}
	}
	t.cache[key] = &tmdbEntry{body: body, expires: time.Now().Add(ttl)}
}

// fallbackOrder is the household's languages: an empty overview or title in the requested language
// is filled from the first of these that has it.
var fallbackOrder = []string{"ru", "uk", "pl", "en"}

func (t *tmdbProxy) fillLanguage(ctx context.Context, path, lang string, body []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return body
	}
	fields := []string{"overview", "title", "name"}
	empty := func() []string {
		var out []string
		for _, f := range fields {
			if v, ok := doc[f]; ok && v == "" {
				out = append(out, f)
			}
		}
		return out
	}
	missing := empty()
	if len(missing) == 0 {
		return body
	}
	for _, l := range fallbackOrder {
		if strings.HasPrefix(strings.ToLower(lang), l) {
			continue
		}
		alt, status := t.get(ctx, path, url.Values{"language": {l}})
		var other map[string]any
		if status != http.StatusOK || json.Unmarshal(alt, &other) != nil {
			continue
		}
		for _, f := range missing {
			if s, ok := other[f].(string); ok && s != "" {
				doc[f] = s
			}
		}
		if missing = empty(); len(missing) == 0 {
			break
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}
