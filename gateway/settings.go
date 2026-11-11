package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// TorrServer settings as code. The gateway checks them at start-up and every 10 minutes.
// TorrServer's "set" replaces the whole settings object and drops every active torrent
// (server/torr/apihelper.go, SetSettings), so the gateway writes only when a value differs, and then
// writes the live object with just the differing keys replaced.

var settingsDrift = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "gateway_torrserver_settings_drift",
	Help: "TorrServer settings that differ from the enforced values after the last check.",
})

var settingsClient = &http.Client{Timeout: 30 * time.Second}

// wantSettings are the enforced values. uploadKB 0 turns upload off (the default); a positive
// value allows upload capped at that many KB/s.
func wantSettings(cacheMB, uploadKB int) map[string]any {
	return map[string]any{
		"CacheSize":                int64(cacheMB) << 20,
		"PreloadCache":             10,
		"ReaderReadAHead":          95,
		"UseDisk":                  false,
		"DisableUpload":            uploadKB == 0,
		"UploadRateLimit":          uploadKB,
		"ConnectionsLimit":         50,
		"DisableUPNP":              true,
		"EnableLPD":                false,
		"EnableBonjour":            false,
		"EnableDLNA":               false,
		"EnableRutorSearch":        false,
		"EnableTorznabSearch":      false,
		"TorrentDisconnectTimeout": 30,
	}
}

func (t *torrServer) settingsCall(ctx context.Context, action string, sets map[string]any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{"action": action, "sets": sets})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/settings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(t.user, t.pass)
	resp, err := settingsClient.Do(req)
	if err != nil {
		return nil, safeErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torrserver settings %s: HTTP %d", action, resp.StatusCode)
	}
	if action != "get" {
		return nil, nil
	}
	var live map[string]any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	return live, dec.Decode(&live)
}

// drift lists the keys whose live value differs from the wanted one, compared as JSON.
func drift(live, want map[string]any) []string {
	var keys []string
	for k, v := range want {
		a, _ := json.Marshal(live[k])
		b, _ := json.Marshal(v)
		if !bytes.Equal(a, b) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (t *torrServer) reconcile(ctx context.Context, want map[string]any) error {
	live, err := t.settingsCall(ctx, "get", nil)
	if err != nil {
		t.ready.Store(false) // unreachable TorrServer: /healthz must say so, not keep the last good answer
		return err
	}
	keys := drift(live, want)
	if len(keys) == 0 {
		settingsDrift.Set(0)
		t.ready.Store(true)
		return nil
	}
	t.ready.Store(false) // TorrServer lost the enforced values (for example a reset volume): hold clients off
	slog.Warn("torrserver settings differ, applying", "keys", keys)
	for _, k := range keys {
		live[k] = want[k]
	}
	if _, err := t.settingsCall(ctx, "set", live); err != nil {
		settingsDrift.Set(float64(len(keys)))
		return err
	}
	if live, err = t.settingsCall(ctx, "get", nil); err != nil {
		return err
	}
	left := drift(live, want)
	settingsDrift.Set(float64(len(left)))
	if len(left) > 0 {
		return fmt.Errorf("torrserver kept %v after set", left)
	}
	t.ready.Store(true)
	return nil
}

// keepSettings reconciles until ctx ends: every 10 minutes, or every 10 s while TorrServer is unreachable.
func (t *torrServer) keepSettings(ctx context.Context, want map[string]any) {
	for {
		wait := 10 * time.Minute
		if err := t.reconcile(ctx, want); err != nil {
			slog.Warn("torrserver settings", "err", err)
			wait = 10 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
