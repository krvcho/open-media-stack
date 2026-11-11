package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	searchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "gateway_search_duration_seconds", Help: "End-to-end Jackett search through Prowlarr.",
		Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 8, 13, 20, 30},
	})
	searchResults = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gateway_search_results", Help: "Results per search by stage.",
		Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250},
	}, []string{"stage"})
	searchErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_search_errors_total", Help: "Failed searches by reason.",
	}, []string{"reason"})
	linkResolutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_torrent_link_resolutions_total", Help: "Opaque release links resolved, by outcome.",
	}, []string{"outcome"})
)

// linkStore maps opaque release ids to Prowlarr download URLs, which contain Prowlarr's API key
// and therefore never leave the server.
type linkStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]linkEntry
	bySrc   map[string]string // one id per release, so repeated searches do not fill the store
}

type linkEntry struct {
	src     string
	body    []byte // .torrent fetched on add, so TorrServer's fetch does not hit the tracker twice
	expires time.Time
}

func newLinkStore(ttl time.Duration, max int) *linkStore {
	return &linkStore{ttl: ttl, max: max, entries: map[string]linkEntry{}, bySrc: map[string]string{}}
}

// put returns the release's id, reusing (and extending) an existing one.
func (s *linkStore) put(src string) string {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.bySrc[src]; ok {
		if e, ok := s.entries[id]; ok && now.Before(e.expires) {
			e.expires = now.Add(s.ttl)
			s.entries[id] = e
			return id
		}
	}
	if len(s.entries) >= s.max {
		for k, e := range s.entries {
			if now.After(e.expires) {
				s.drop(k, e)
			}
		}
		// Limit: drops arbitrary entries when still full; with one id per release this needs 50 000 distinct
		// releases within 24 h. An LRU only if it ever triggers.
		for k, e := range s.entries {
			if len(s.entries) < s.max {
				break
			}
			s.drop(k, e)
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	s.entries[id] = linkEntry{src: src, expires: now.Add(s.ttl)}
	s.bySrc[src] = id
	return id
}

func (s *linkStore) drop(id string, e linkEntry) {
	delete(s.entries, id)
	if s.bySrc[e.src] == id {
		delete(s.bySrc, e.src)
	}
}

func (s *linkStore) get(id string) (linkEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || time.Now().After(e.expires) {
		if ok {
			s.drop(id, e)
		}
		return linkEntry{}, false
	}
	return e, true
}

func (s *linkStore) setBody(id string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[id]; ok {
		e.body = body
		s.entries[id] = e
	}
}

type prowlarr struct {
	base, key string
	client    *http.Client // follows no redirects: a redirect to magnet: is the answer, not a hop
}

func newProwlarr(base, key string) *prowlarr {
	return &prowlarr{base: base, key: key, client: &http.Client{
		// Lampa's longest parser timeout is 120 s; the client decides when to give up, not the gateway.
		Timeout:       120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// prowlarrRelease is the subset of Prowlarr's ReleaseResource we read.
type prowlarrRelease struct {
	Title       string `json:"title"`
	Indexer     string `json:"indexer"`
	Size        int64  `json:"size"`
	PublishDate string `json:"publishDate"`
	Seeders     *int   `json:"seeders"`
	Leechers    *int   `json:"leechers"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
	GUID        string `json:"guid"`
	InfoURL     string `json:"infoUrl"`
	Protocol    string `json:"protocol"`
	Categories  []struct {
		ID int `json:"id"`
	} `json:"categories"`
}

// search asks one Prowlarr indexer.
func (p *prowlarr) search(ctx context.Context, query, kind string, categories []string, indexerID int) ([]prowlarrRelease, error) {
	q := url.Values{"query": {query}, "type": {kind}, "indexerIds": {fmt.Sprint(indexerID)}}
	for _, c := range categories {
		q.Add("categories", c)
	}
	// The client's context cancels the Prowlarr search when Lampa gives up waiting.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/api/v1/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", p.key) // header, not query string: keeps the key out of access logs
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr search: %w", safeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr search: HTTP %d", resp.StatusCode)
	}
	var out []prowlarrRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// jackettResult is the Jackett JSON shape stock Lampa reads.
type jackettResult struct {
	Title        string `json:"Title"`
	Tracker      string `json:"Tracker"`
	Size         int64  `json:"Size"`
	PublishDate  string `json:"PublishDate"`
	Seeders      int    `json:"Seeders"`
	Peers        int    `json:"Peers"`
	MagnetUri    string `json:"MagnetUri"`
	Details      string `json:"Details,omitempty"`
	CategoryDesc string `json:"CategoryDesc,omitempty"`
	// Parsed from the title; Lampa shows and filters by these (lampa-source torrents.js).
	Info      jackettInfo `json:"info"`
	Languages []string    `json:"languages,omitempty"`
	Rank      int         `json:"Rank"` // our order; Lampa sorts by it when torrents_sort is "Rank"
}

type jackettInfo struct {
	Quality   int      `json:"quality,omitempty"`
	Voices    []string `json:"voices,omitempty"`
	VideoType string   `json:"videotype,omitempty"`
	Seasons   []int    `json:"seasons,omitempty"`
}

// toJackett converts Prowlarr releases. Every link becomes an opaque gateway URL; Prowlarr's own
// download URLs (with its API key) stay in the store.
func toJackett(in []prowlarrRelease, links *linkStore, base string, c card, minSeeders, max int) []jackettResult {
	type candidate struct {
		res jackettResult
		src string
	}
	var keep []candidate
	for _, r := range in {
		if r.Protocol != "" && r.Protocol != "torrent" {
			continue
		}
		src := r.DownloadURL
		if src == "" {
			src = r.MagnetURL
		}
		if src == "" && strings.HasPrefix(r.GUID, "magnet:") {
			src = r.GUID
		}
		if src == "" {
			continue
		}
		if r.Seeders != nil && *r.Seeders < minSeeders { // zero upload makes seeders matter
			continue
		}
		// Cheap checks first: parsing is regex work, and a search brings hundreds of releases.
		if !c.named(r.Title) { // other titles: never what was asked for
			continue
		}
		p := defaultVocab.parse(r.Title)
		if p.Junk || !c.inYears(p) { // books, music, games; other years
			continue
		}
		videoType := "sdr"
		switch {
		case p.DV:
			videoType = "dv"
		case p.HDR:
			videoType = "hdr"
		}
		res := jackettResult{
			Info:      jackettInfo{Quality: p.Resolution, Voices: p.Studios, VideoType: videoType, Seasons: p.Seasons},
			Languages: p.Languages,
			Title:     r.Title, Tracker: r.Indexer, Size: r.Size, PublishDate: r.PublishDate,
		}
		if r.Seeders != nil {
			res.Seeders = *r.Seeders
		}
		if r.Leechers != nil {
			res.Peers = *r.Leechers
		}
		res.Rank = int(math.Round(score(p, res.Seeders, c.prefs)))
		if strings.HasPrefix(r.InfoURL, "http") && !strings.Contains(strings.ToLower(r.InfoURL), "apikey") {
			res.Details = r.InfoURL
		}
		keep = append(keep, candidate{res, src})
	}
	sort.SliceStable(keep, func(a, b int) bool {
		if keep[a].res.Rank != keep[b].res.Rank {
			return keep[a].res.Rank > keep[b].res.Rank
		}
		return keep[a].res.Seeders > keep[b].res.Seeders
	})
	if len(keep) > max {
		keep = keep[:max]
	}
	out := make([]jackettResult, 0, len(keep))
	for _, k := range keep {
		k.res.MagnetUri = base + "/t/" + links.put(k.src) // only for what is returned
		out = append(out, k.res)
	}
	return out
}

// handleSearch answers GET .../api/v2.0/indexers/{status}/results?Query=…&Category[]=…&is_serial=…
func (g *gateway) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("Query"))
	if query == "" {
		query = strings.TrimSpace(q.Get("title"))
	}
	if query == "" {
		http.Error(w, "missing Query", http.StatusBadRequest)
		return
	}
	var cats []string
	for _, v := range q["Category[]"] {
		for _, c := range strings.Split(v, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cats = append(cats, c)
			}
		}
	}
	if len(cats) > 0 {
		// Some indexers cannot tell a film from anything else in their search results, and Prowlarr files
		// every result of theirs under 8000 "Other". Without it a Movies or TV search drops
		// them all; the noise this lets in is removed by card matching.
		cats = append(cats, "8000")
	}
	kind := "search"
	if q.Get("is_serial") == "2" {
		kind = "tvsearch"
	}
	c := cardFrom(q)
	if rest := r.PathValue("rest"); strings.HasPrefix(rest, "jackett/p/") {
		seg, _, _ := strings.Cut(strings.TrimPrefix(rest, "jackett/p/"), "/")
		c.prefs = prefsFrom(seg)
	}
	keep := func(r prowlarrRelease) bool { // the cheap part of toJackett's filter
		return (r.Seeders == nil || *r.Seeders >= g.cfg.MinSeeders) && c.named(r.Title)
	}
	releases, partial, err := g.fan.search(r.Context(), query, kind, cats, keep)
	if partial {
		searchPartial.Inc()
	}
	if errors.Is(err, context.Canceled) {
		searchErrors.WithLabelValues("client_gone").Inc()
		return
	}
	if err != nil {
		searchErrors.WithLabelValues("prowlarr").Inc()
		slog.Warn("search failed", "err", err)
		http.Error(w, "search backend unavailable", http.StatusBadGateway)
		return
	}
	results := toJackett(releases, g.links, clientBase(r, g.cfg.Token), c, g.cfg.MinSeeders, g.cfg.MaxResults)
	searchResults.WithLabelValues("raw").Observe(float64(len(releases)))
	searchResults.WithLabelValues("returned").Observe(float64(len(results)))
	searchDuration.Observe(time.Since(start).Seconds())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*") // stock TV apps call from their own origin
	_ = json.NewEncoder(w).Encode(map[string]any{"Results": results})
}

var errUnknownLink = errors.New("unknown or expired release link")

// resolveLink turns an opaque id into something TorrServer can add without seeing a key:
// the magnet a Prowlarr download redirects to, or the gateway's internal .torrent URL.
func (g *gateway) resolveLink(id string) (string, error) {
	body, magnet, err := g.fetchTorrent(id)
	switch {
	case errors.Is(err, errUnknownLink):
		linkResolutions.WithLabelValues("unknown").Inc()
		return "", err
	case err != nil:
		linkResolutions.WithLabelValues("error").Inc()
		return "", err
	case magnet != "":
		linkResolutions.WithLabelValues("magnet").Inc()
		return magnet, nil
	}
	g.links.setBody(id, body)
	linkResolutions.WithLabelValues("torrent").Inc()
	return g.cfg.InternalURL + "/internal/t/" + id, nil
}

// fetchTorrent downloads the release from Prowlarr server-side. A magnet redirect is returned as is.
func (g *gateway) fetchTorrent(id string) (body []byte, magnet string, err error) {
	e, ok := g.links.get(id)
	if !ok {
		return nil, "", errUnknownLink
	}
	if e.body != nil {
		return e.body, "", nil
	}
	if strings.HasPrefix(e.src, "magnet:") {
		return nil, e.src, nil
	}
	resp, err := g.pr.client.Get(e.src)
	if err != nil {
		return nil, "", fmt.Errorf("prowlarr download: %w", safeErr(err)) // e.src carries Prowlarr's API key
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && strings.HasPrefix(loc, "magnet:") {
		return nil, loc, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("prowlarr download: HTTP %d", resp.StatusCode)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return body, "", err
}

func writeTorrent(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// handleInternalTorrent serves the .torrent to TorrServer on the internal network only.
func (g *gateway) handleInternalTorrent(w http.ResponseWriter, r *http.Request) {
	body, magnet, err := g.fetchTorrent(r.PathValue("id"))
	switch {
	case err != nil:
		slog.Warn("internal torrent", "err", err)
		http.NotFound(w, r)
	case magnet != "":
		http.Error(w, "release is a magnet", http.StatusConflict)
	default:
		writeTorrent(w, body)
	}
}

// handlePublicTorrent lets a household device fetch a release directly (for example an external torrent app).
func (g *gateway) handlePublicTorrent(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, magnet, err := g.fetchTorrent(id)
		switch {
		case err != nil:
			http.NotFound(w, r)
		case magnet != "":
			http.Redirect(w, r, magnet, http.StatusFound)
		default:
			writeTorrent(w, body)
		}
	}
}
