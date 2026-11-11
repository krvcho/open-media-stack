package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Swarm and upstream metrics, read when Prometheus scrapes: no background polling, and a scrape
// every 30 s costs one TorrServer list and four small probes.

var (
	upDesc      = prometheus.NewDesc("gateway_upstream_up", "1 if the upstream answered its probe.", []string{"service"}, nil)
	probeDesc   = prometheus.NewDesc("gateway_upstream_probe_duration_seconds", "Duration of the upstream probe.", []string{"service"}, nil)
	torrsDesc   = prometheus.NewDesc("gateway_torrents_active", "Torrents loaded in TorrServer.", nil, nil)
	peersDesc   = prometheus.NewDesc("gateway_torrent_peers", "Peers over all loaded torrents.", []string{"state"}, nil)
	dlDesc      = prometheus.NewDesc("gateway_torrent_download_bytes_per_second", "TorrServer's download rate over all torrents.", nil, nil)
	uploadDesc  = prometheus.NewDesc("gateway_torrent_upload_bytes_total", "Piece data uploaded to peers; must stay 0 (upload is off).", nil, nil)
	preloadDesc = prometheus.NewDesc("gateway_torrent_preloaded_bytes", "Bytes in TorrServer's cache over all torrents.", nil, nil)
)

// tsTorrent is the subset of TorrServer's TorrentStatus we read (server/torr/state/state.go). upload_speed is
// not used: TorrServer computes it from all bytes written, protocol messages included.
type tsTorrent struct {
	Hash             string  `json:"hash"`
	DownloadSpeed    float64 `json:"download_speed"`
	TotalPeers       int     `json:"total_peers"`
	ActivePeers      int     `json:"active_peers"`
	ConnectedSeeders int     `json:"connected_seeders"`
	BytesWrittenData int64   `json:"bytes_written_data"`
	PreloadedBytes   int64   `json:"preloaded_bytes"`
}

type probeCollector struct {
	g      *gateway
	client *http.Client

	mu       sync.Mutex
	uploaded float64          // counter: sum of every torrent's growth in uploaded piece data
	last     map[string]int64 // per loaded torrent, its uploaded piece data at the previous scrape
}

func newProbeCollector(g *gateway) *probeCollector {
	return &probeCollector{g: g, client: &http.Client{Timeout: 3 * time.Second}, last: map[string]int64{}}
}

func (c *probeCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{upDesc, probeDesc, torrsDesc, peersDesc, dlDesc, uploadDesc, preloadDesc} {
		ch <- d
	}
}

func (c *probeCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	probes := map[string]func(context.Context) error{
		"prowlarr":   func(ctx context.Context) error { return c.get(ctx, c.g.cfg.ProwlarrURL+"/ping") },
		"torrserver": func(ctx context.Context) error { return c.torrents(ctx, ch) },
		"lampac":     func(ctx context.Context) error { return c.get(ctx, c.g.cfg.LampacURL+"/") },
	}
	if c.g.cfg.VPNControlURL != "" {
		probes["vpn"] = func(ctx context.Context) error {
			if c.g.vpnStatus(ctx).Running {
				return nil
			}
			return fmt.Errorf("tunnel down")
		}
	}
	var wg sync.WaitGroup
	for name, probe := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			up := 0.0
			if probe(ctx) == nil {
				up = 1
			}
			ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, up, name)
			ch <- prometheus.MustNewConstMetric(probeDesc, prometheus.GaugeValue, time.Since(start).Seconds(), name)
		}()
	}
	wg.Wait()
}

// get succeeds on any answer below 500: the probe asks "is it serving", not "is this route allowed".
func (c *probeCollector) get(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// torrents lists TorrServer's loaded torrents, which doubles as its probe.
func (c *probeCollector) torrents(ctx context.Context, ch chan<- prometheus.Metric) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.g.cfg.TorrServerURL+"/torrents", bytes.NewReader([]byte(`{"action":"list"}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.g.cfg.TorrServerUser, c.g.cfg.TorrServerPass)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var list []tsTorrent
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return err
	}

	var total, active, seeders int
	var dl, preloaded float64
	for _, t := range list {
		total += t.TotalPeers
		active += t.ActivePeers
		seeders += t.ConnectedSeeders
		dl += t.DownloadSpeed
		preloaded += float64(t.PreloadedBytes)
	}
	ch <- prometheus.MustNewConstMetric(torrsDesc, prometheus.GaugeValue, float64(len(list)))
	ch <- prometheus.MustNewConstMetric(peersDesc, prometheus.GaugeValue, float64(total), "total")
	ch <- prometheus.MustNewConstMetric(peersDesc, prometheus.GaugeValue, float64(active), "active")
	ch <- prometheus.MustNewConstMetric(peersDesc, prometheus.GaugeValue, float64(seeders), "seeders")
	ch <- prometheus.MustNewConstMetric(dlDesc, prometheus.GaugeValue, dl)
	ch <- prometheus.MustNewConstMetric(preloadDesc, prometheus.GaugeValue, preloaded)
	ch <- prometheus.MustNewConstMetric(uploadDesc, prometheus.CounterValue, c.addUploaded(list))
	return nil
}

// addUploaded turns per-torrent upload totals, which vanish when a torrent is dropped, into one counter that
// only grows. A torrent first seen with data already uploaded counts in full. Limit: upload by a
// torrent added and dropped between two scrapes is missed; poll TorrServer on its own timer if that matters.
func (c *probeCollector) addUploaded(list []tsTorrent) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]int64, len(list))
	for _, t := range list {
		d := t.BytesWrittenData - c.last[t.Hash]
		if d < 0 { // dropped and added again between scrapes: its total restarted
			d = t.BytesWrittenData
		}
		c.uploaded += float64(d)
		seen[t.Hash] = t.BytesWrittenData
	}
	c.last = seen
	return c.uploaded
}
