package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Search fan-out (search latency target: p95 within 5 s). Prowlarr answers a search only when
// its slowest indexer has, and indexers behind FlareSolverr take 10–60 s. So the gateway asks every enabled
// indexer on its own and in parallel, and answers at the deadline with what has arrived. Slow indexers keep
// running in the background; their results land in a short-lived cache, so the same search a moment later
// is complete.

var (
	indexerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gateway_indexer_search_duration_seconds", Help: "Search time per Prowlarr indexer.",
		Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 20, 40, 90},
	}, []string{"indexer"})
	indexerSearches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_indexer_searches_total", Help: "Searches per Prowlarr indexer, by outcome.",
	}, []string{"indexer", "outcome"})
	searchPartial = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_search_partial_total", Help: "Searches answered before every indexer had finished.",
	})
)

type indexer struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Enable   bool   `json:"enable"`
	Protocol string `json:"protocol"`
}

type searchJob struct {
	mu       sync.Mutex
	releases []prowlarrRelease
	pending  int
	created  time.Time
	ctx      context.Context    // cancelled when newer searches push this one out (maxRunning)
	cancel   context.CancelFunc // stops the indexer searches still running
}

func (j *searchJob) state() (n, pending int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.releases), j.pending
}

type fanout struct {
	pr         *prowlarr
	soft       time.Duration // answer by then if at least `enough` releases have arrived; 1.5 s plus parsing and transfer kept p50 under 2 s on the Pi
	enough     int           // usable releases (seeder floor, card name) that make a first page worth showing
	deadline   time.Duration // answer with whatever has arrived by then (target: p95 within 5 s)
	hardCap    time.Duration // when nothing has arrived, wait at most this long (Lampa gives up at 30 s)
	perIndexer time.Duration // one indexer's search, running in the background
	ttl        time.Duration // how long a search's results are reused
	maxRunning int           // searches still running in the background; older ones are cancelled beyond this
	maxJobs    int           // cached searches kept

	mu   sync.Mutex
	jobs map[string]*searchJob

	listMu    sync.Mutex
	indexers  []indexer
	listedAt  time.Time
	refreshes bool // a background refresh of the indexer list is running
}

func newFanout(pr *prowlarr) *fanout {
	return &fanout{pr: pr, soft: 1500 * time.Millisecond, enough: 10, deadline: 4500 * time.Millisecond,
		hardCap: 25 * time.Second, perIndexer: 90 * time.Second, ttl: 10 * time.Minute,
		maxRunning: 4, maxJobs: 200, jobs: map[string]*searchJob{}}
}

// list returns Prowlarr's enabled indexers. Only the very first call waits for Prowlarr; afterwards a list
// older than 5 minutes is used as it is while a fresh one is fetched in the background.
func (f *fanout) list() ([]indexer, error) {
	f.listMu.Lock()
	if f.indexers != nil {
		if time.Since(f.listedAt) > 5*time.Minute && !f.refreshes {
			f.refreshes = true
			go func() {
				list, err := f.pr.indexers()
				f.listMu.Lock()
				defer f.listMu.Unlock()
				f.refreshes = false
				if err != nil {
					slog.Warn("prowlarr indexer list", "err", err)
					return
				}
				f.indexers, f.listedAt = list, time.Now()
			}()
		}
		list := f.indexers
		f.listMu.Unlock()
		return list, nil
	}
	f.listMu.Unlock()
	list, err := f.pr.indexers()
	if err != nil {
		return nil, err
	}
	f.listMu.Lock()
	f.indexers, f.listedAt = list, time.Now()
	f.listMu.Unlock()
	return list, nil
}

// search returns the releases that have arrived, and whether some indexers are still running. The clock
// starts with the search, not the request, so asking again for a running search answers at once. keep says
// which releases the caller can use (nil: all); the early answer waits for `enough` of those, so a burst of
// releases for other titles cannot end the wait with nothing useful.
func (f *fanout) search(ctx context.Context, query, kind string, cats []string, keep func(prowlarrRelease) bool) ([]prowlarrRelease, bool, error) {
	job, err := f.job(kind+"\x00"+query+"\x00"+strings.Join(cats, ","), query, kind, cats)
	if err != nil {
		return nil, false, err
	}
	tick := time.NewTicker(100 * time.Millisecond) // Limit: polling adds up to 100 ms; a broadcast channel if that ever matters
	defer tick.Stop()
	seen, useful := 0, 0
	for {
		job.mu.Lock()
		for ; seen < len(job.releases); seen++ {
			if keep == nil || keep(job.releases[seen]) {
				useful++
			}
		}
		n, pending := len(job.releases), job.pending
		job.mu.Unlock()
		waited := time.Since(job.created)
		if pending == 0 || (useful >= f.enough && waited >= f.soft) || (n > 0 && waited >= f.deadline) || waited >= f.hardCap {
			job.mu.Lock()
			out := append([]prowlarrRelease(nil), job.releases...)
			job.mu.Unlock()
			return out, pending > 0, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-tick.C:
		}
	}
}

// cached returns a reusable search for key. A finished search that found nothing is not reused: the
// indexers may have been down.
func (f *fanout) cached(key string) *searchJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.jobs[key]; ok && time.Since(j.created) <= f.ttl {
		if n, pending := j.state(); n > 0 || pending > 0 {
			return j
		}
	}
	return nil
}

// job returns the running or cached search for key, or starts one on every enabled indexer.
func (f *fanout) job(key, query, kind string, cats []string) (*searchJob, error) {
	if j := f.cached(key); j != nil {
		return j, nil
	}
	indexers, err := f.list() // may wait for Prowlarr, so outside f.mu
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.jobs[key]; ok && time.Since(j.created) <= f.ttl {
		if n, pending := j.state(); n > 0 || pending > 0 {
			return j, nil // started by a concurrent request meanwhile
		}
	}
	f.prune()
	ctx, cancel := context.WithCancel(context.Background())
	j := &searchJob{pending: len(indexers), created: time.Now(), ctx: ctx, cancel: cancel}
	f.jobs[key] = j
	for _, ix := range indexers {
		go f.run(j, ix, query, kind, cats)
	}
	return j, nil
}

// prune drops expired searches, cancels running ones beyond maxRunning (oldest first) and keeps at most
// maxJobs. Called with f.mu held, before a new search starts.
func (f *fanout) prune() {
	var running []*searchJob
	oldestKey, oldest := "", time.Time{}
	for k, j := range f.jobs {
		if time.Since(j.created) > f.ttl {
			j.cancel()
			delete(f.jobs, k)
			continue
		}
		if _, pending := j.state(); pending > 0 && j.ctx.Err() == nil {
			running = append(running, j)
		}
		if oldest.IsZero() || j.created.Before(oldest) {
			oldestKey, oldest = k, j.created
		}
	}
	sort.Slice(running, func(a, b int) bool { return running[a].created.Before(running[b].created) })
	for i := 0; i <= len(running)-f.maxRunning; i++ {
		running[i].cancel() // its results so far stay usable
	}
	if len(f.jobs) >= f.maxJobs {
		f.jobs[oldestKey].cancel()
		delete(f.jobs, oldestKey)
	}
}

func (f *fanout) run(j *searchJob, ix indexer, query, kind string, cats []string) {
	ctx, cancel := context.WithTimeout(j.ctx, f.perIndexer)
	defer cancel()
	start := time.Now()
	rs, err := f.pr.search(ctx, query, kind, cats, ix.ID)
	indexerDuration.WithLabelValues(ix.Name).Observe(time.Since(start).Seconds())
	outcome := "ok"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		outcome = "timeout"
	case errors.Is(err, context.Canceled):
		outcome = "cancelled" // pushed out by newer searches
	case err != nil:
		outcome = "error"
	}
	indexerSearches.WithLabelValues(ix.Name, outcome).Inc()
	if err != nil && outcome != "cancelled" {
		slog.Warn("indexer search failed", "indexer", ix.Name, "err", err)
	}
	j.mu.Lock()
	j.releases = append(j.releases, rs...)
	j.pending--
	j.mu.Unlock()
}

// indexers lists Prowlarr's enabled torrent indexers.
func (p *prowlarr) indexers() ([]indexer, error) {
	req, err := http.NewRequest(http.MethodGet, p.base+"/api/v1/indexer", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", p.key)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr indexers: %w", safeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr indexers: HTTP %d", resp.StatusCode)
	}
	var all []indexer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&all); err != nil {
		return nil, err
	}
	out := []indexer{}
	for _, ix := range all {
		if ix.Enable && (ix.Protocol == "" || ix.Protocol == "torrent") {
			out = append(out, ix)
		}
	}
	return out, nil
}
