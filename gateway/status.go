package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Status for the plugin's top-bar indicator: is TorrServer ready, and does its traffic leave through the VPN?
// The VPN answer comes from gluetun's control server, which reports the tunnel's state and the public IP seen
// from inside the namespace TorrServer shares. Both routes are open to the stack's network only.

type vpnStatus struct {
	Running bool   `json:"running"`
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
}

type stackStatus struct {
	TorrServer bool       `json:"torrserver"` // enforced settings live
	VPN        *vpnStatus `json:"vpn"`        // null: no VPN configured, TorrServer uses the host's address
}

var vpnClient = &http.Client{Timeout: 3 * time.Second}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := vpnClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(v)
}

func (g *gateway) vpnStatus(ctx context.Context) *vpnStatus {
	if g.cfg.VPNControlURL == "" {
		return nil
	}
	var st struct {
		Status string `json:"status"`
	}
	var ip struct {
		PublicIP string `json:"public_ip"`
		Country  string `json:"country"`
		City     string `json:"city"`
	}
	v := &vpnStatus{}
	if getJSON(ctx, g.cfg.VPNControlURL+"/v1/vpn/status", &st) != nil || st.Status != "running" {
		return v
	}
	if getJSON(ctx, g.cfg.VPNControlURL+"/v1/publicip/ip", &ip) != nil || ip.PublicIP == "" {
		return v
	}
	return &vpnStatus{Running: true, IP: ip.PublicIP, Country: ip.Country, City: ip.City}
}

func (g *gateway) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*") // stock TV apps call from their own origin
	_ = json.NewEncoder(w).Encode(stackStatus{TorrServer: g.ts.ready.Load(), VPN: g.vpnStatus(r.Context())})
}
