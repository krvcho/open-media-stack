package main

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Client playback beacon: our plugin reports what the viewer sees in Lampa's own player (browser,
// Tizen, webOS). External players on Android and iPhone report nothing back, so they are not counted.
var (
	clientTTFF = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "client_ttff_seconds",
		Help:    "Time from pressing play in Lampa's own player to its first frame.",
		Buckets: []float64{0.5, 1, 2, 3, 5, 7.5, 10, 15, 20, 30, 60},
	}, []string{"platform", "source"})
	clientRebuffers = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_rebuffer_total", Help: "Stalls after the first frame that were not caused by a seek.",
	}, []string{"platform", "source"})
	clientErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_playback_errors_total", Help: "Fatal errors in Lampa's own player.",
	}, []string{"platform", "source"})
)

// Label values are the client's word, so they are mapped onto fixed sets: no client can grow the series.
var (
	beaconPlatforms = map[string]bool{"browser": true, "tizen": true, "webos": true, "android": true,
		"apple": true, "apple_tv": true, "netcast": true, "orsay": true, "philips": true, "nw": true, "electron": true}
	beaconSources = map[string]bool{"torrent": true, "online": true}
)

func oneOf(set map[string]bool, v string) string {
	if set[v] {
		return v
	}
	return "other"
}

type beacon struct {
	Event    string  `json:"e"` // ttff, rebuffer, error
	Seconds  float64 `json:"s"` // ttff only
	Platform string  `json:"p"`
	Source   string  `json:"src"`
}

func handleBeacon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var b beacon
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&b); err != nil {
		http.Error(w, "bad beacon", http.StatusBadRequest)
		return
	}
	p, s := oneOf(beaconPlatforms, b.Platform), oneOf(beaconSources, b.Source)
	switch b.Event {
	case "ttff":
		if b.Seconds <= 0 || b.Seconds > 600 { // a tab left open on a paused player is not a start time
			http.Error(w, "bad ttff", http.StatusBadRequest)
			return
		}
		clientTTFF.WithLabelValues(p, s).Observe(b.Seconds)
	case "rebuffer":
		clientRebuffers.WithLabelValues(p, s).Inc()
	case "error":
		clientErrors.WithLabelValues(p, s).Inc()
	default:
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
