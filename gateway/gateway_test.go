package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const testToken = "0123456789abcdef0123456789abcdef"

func intp(i int) *int { return &i }

func TestToJackettKeepsProwlarrURLsServerSide(t *testing.T) {
	links := newLinkStore(time.Hour, 100)
	in := []prowlarrRelease{
		{Title: "A", Indexer: "x", Size: 10, Seeders: intp(5), Leechers: intp(2), Protocol: "torrent",
			DownloadURL: "http://prowlarr:9696/1/download?apikey=SECRETKEY&link=abc", InfoURL: "https://example.org/a"},
		{Title: "B", Protocol: "torrent", GUID: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)},
		{Title: "usenet", Protocol: "usenet", DownloadURL: "http://prowlarr/nzb?apikey=SECRETKEY"},
		{Title: "no link", Protocol: "torrent"},
	}
	out := toJackett(in, links, "http://gw/h/tok", card{}, 0, 50)
	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}
	js, _ := json.Marshal(out)
	if bytes.Contains(js, []byte("SECRETKEY")) || bytes.Contains(js, []byte("prowlarr")) {
		t.Fatalf("Prowlarr URL leaked into client response: %s", js)
	}
	if !strings.HasPrefix(out[0].MagnetUri, "http://gw/h/tok/t/") || out[0].Seeders != 5 || out[0].Peers != 2 {
		t.Fatalf("bad mapping: %+v", out[0])
	}
	id := strings.TrimPrefix(out[0].MagnetUri, "http://gw/h/tok/t/")
	if e, ok := links.get(id); !ok || !strings.Contains(e.src, "SECRETKEY") {
		t.Fatalf("opaque id does not map back to the Prowlarr URL")
	}
}

func TestLinkStoreExpiryAndCap(t *testing.T) {
	s := newLinkStore(time.Millisecond, 2)
	id := s.put("a")
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.get(id); ok {
		t.Fatal("expired entry still returned")
	}
	s = newLinkStore(time.Hour, 2)
	if a, b := s.put("x"), s.put("x"); a != b {
		t.Fatal("the same release must keep one id across searches")
	}
	for i := 0; i < 10; i++ {
		s.put(fmt.Sprint("r", i))
	}
	if len(s.entries) > 2 || len(s.bySrc) > 2 {
		t.Fatalf("store grew past its cap: %d entries, %d sources", len(s.entries), len(s.bySrc))
	}
}

func TestRewriteLink(t *testing.T) {
	ts := &torrServer{resolve: func(id string) (string, error) { return "resolved:" + id, nil }}
	id := strings.Repeat("ab", 16)
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"magnet:?xt=urn:btih:" + strings.Repeat("a", 40), "magnet:?xt=urn:btih:" + strings.Repeat("a", 40), true},
		{"magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=Big+Film&xl=123" +
			"&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce" + // public: kept
			"&tr=http%3A%2F%2Fprowlarr%3A9696%2F1%2Fdownload" + // Docker service name
			"&tr=http%3A%2F%2F192.168.1.1%2Fannounce&tr=udp%3A%2F%2F%5B%3A%3A1%5D%3A80" + // LAN, loopback
			"&tr=http%3A%2F%2F100.101.102.103%2Fa&tr=http%3A%2F%2Flocalhost%2Fa&tr=file%3A%2F%2F%2Fetc%2Fpasswd" +
			"&xs=http%3A%2F%2Fgateway%3A8081%2Finternal%2Ft%2Fx&ws=http%3A%2F%2F10.0.0.1%2Ff&x.pe=10.0.0.2%3A6881",
			"magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=Big+Film&xl=123&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce", true},
		{"magnet:?dn=no-hash", "", false},
		{"magnet:?xt=urn:btih:zz", "", false},
		{strings.Repeat("A", 40), strings.Repeat("A", 40), true},
		{"http://gw:8080/h/" + testToken + "/t/" + id, "resolved:" + id, true},
		{"http://prowlarr:9696/1/download?apikey=x", "", false},
		{"http://169.254.169.254/latest/meta-data", "", false},
		{"file:///etc/passwd", "", false},
	}
	for _, c := range cases {
		got, err := ts.rewriteLink(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("rewriteLink(%q) = %q, %v; want %q ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestTorrServerAllowlist(t *testing.T) {
	allowed := [][2]string{{"GET", "/echo"}, {"POST", "/torrents"}, {"GET", "/stream/f.mkv"}, {"HEAD", "/play/h/1"}, {"GET", "/ffp/h/1"}}
	refused := [][2]string{{"GET", "/shutdown"}, {"POST", "/torrent/upload"}, {"GET", "/settings"}, {"POST", "/gst/settings"}, {"GET", "/mcp"}, {"DELETE", "/torrents"}}
	for _, a := range allowed {
		if !tsAllowed(a[0], a[1]) {
			t.Errorf("%s %s should be allowed", a[0], a[1])
		}
	}
	for _, a := range refused {
		if tsAllowed(a[0], a[1]) {
			t.Errorf("%s %s should be refused", a[0], a[1])
		}
	}
}

func TestCheckBody(t *testing.T) {
	ts := &torrServer{resolve: func(id string) (string, error) { return "http://gateway:8081/internal/t/" + id, nil }}
	if _, _, err := ts.checkBody("/settings", []byte(`{"action":"set","sets":{}}`)); err == nil {
		t.Error("settings write must be refused")
	}
	id := strings.Repeat("cd", 16)
	out, _, err := ts.checkBody("/torrents", []byte(`{"action":"add","link":"http://gw/h/tok/t/`+id+`","title":"x"}`))
	if err != nil || !bytes.Contains(out, []byte("/internal/t/"+id)) || !bytes.Contains(out, []byte(`"title":"x"`)) {
		t.Errorf("add not rewritten: %s %v", out, err)
	}
	if _, _, err := ts.checkBody("/torrents", []byte(`{"action":"add","link":"https://evil.example/x.torrent"}`)); err == nil {
		t.Error("foreign link must be refused")
	}
}

// A failed Prowlarr download must not echo its URL (which carries the API key) to the client or the log.
func TestFailedDownloadLeaksNothing(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	g := &gateway{cfg: config{InternalURL: "http://gateway:8081"}, links: newLinkStore(time.Hour, 10),
		pr: newProwlarr("http://127.0.0.1:1", "CANARYKEY")}
	id := g.links.put("http://127.0.0.1:1/1/download?apikey=CANARYKEY&link=x")
	ts := &torrServer{base: "http://127.0.0.1:1", resolve: g.resolveLink}
	ts.ready.Store(true)

	rec := httptest.NewRecorder()
	body := `{"action":"add","link":"http://gw/h/x/t/` + id + `"}`
	ts.handle("/torrents")(rec, httptest.NewRequest(http.MethodPost, "http://gw/h/x/ts/torrents", strings.NewReader(body)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("add with failing download = %d, want 502", rec.Code)
	}
	internal := httptest.NewRecorder()
	g.internalHandler().ServeHTTP(internal, httptest.NewRequest(http.MethodGet, "http://gateway:8081/internal/t/"+id, nil))

	all := rec.Body.String() + internal.Body.String() + logs.String()
	if strings.Contains(all, "CANARYKEY") || strings.Contains(all, "apikey") {
		t.Fatalf("Prowlarr download URL leaked:\n%s", all)
	}
}

// A Movies search must also ask Prowlarr for "Other" (8000), or indexers that file everything under it return nothing.
func TestSearchAsksForOtherCategory(t *testing.T) {
	var got []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"a","enable":true,"protocol":"torrent"}]`))
			return
		}
		got = r.URL.Query()["categories"]
		_, _ = w.Write([]byte("[]"))
	}))
	defer fake.Close()
	pr := newProwlarr(fake.URL, "k")
	g := &gateway{cfg: config{Token: testToken}, links: newLinkStore(time.Hour, 10), pr: pr, fan: newFanout(pr)}
	rec := httptest.NewRecorder()
	g.handleSearch(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/x/jackett/api/v2.0/indexers/all/results?Query=Nova&Category[]=2000", nil))
	if rec.Code != http.StatusOK || strings.Join(got, ",") != "2000,8000" {
		t.Fatalf("status %d, categories sent to Prowlarr %v, want [2000 8000]", rec.Code, got)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.WriteHeader(&tar.Header{Name: "lampa-x/link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(bytes.NewReader(tarGz(t, map[string]string{"lampa-x/index.html": "<head>", "lampa-x/css/app.css": "c"})), dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "css", "app.css")); err != nil || string(b) != "c" {
		t.Fatalf("top-level directory not stripped: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link")); err == nil {
		t.Fatal("symlink must be skipped")
	}
	if err := extractTarGz(bytes.NewReader(tarGz(t, map[string]string{"lampa-x/../../evil": "x"})), t.TempDir()); err == nil {
		t.Fatal("path traversal must be refused")
	}
}

func TestInjectSettings(t *testing.T) {
	out, err := injectSettings([]byte("<html><head><title>x</title>"))
	if err != nil || !bytes.HasPrefix(out, []byte("<html><head>\n<script>window.lampa_settings=")) {
		t.Fatalf("settings not injected after <head>: %s %v", out, err)
	}
	if _, err := injectSettings([]byte("<html>")); err == nil {
		t.Fatal("missing <head> must be an error")
	}
}

func TestTokenGate(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<head></head>"), 0o644)
	g := &gateway{cfg: config{Token: testToken, LampacPort: "9118", LampacID: "lampacid0123456789"}, lampa: dir}
	h := g.publicHandler()
	for path, want := range map[string]int{
		"/h/" + testToken + "/":                        http.StatusOK,
		"/h/wrong-token-wrong-token-wrong-tok/":        http.StatusNotFound,
		"/h/" + testToken + "/plugins/modification.js": http.StatusOK,
		"/": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw"+path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw:8080/h/"+testToken+"/plugins/modification.js", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"http://gw:8080/h/`+testToken+`"`) || !strings.Contains(body, `"http://gw:9118"`) {
		t.Errorf("modification.js has wrong URLs:\n%s", body)
	}
	if !strings.Contains(body, `set('internal_torrclient', true)`) {
		t.Error("modification.js must route Android playback through TorrServer")
	}
	if !strings.Contains(body, "base + '/beacon'") {
		t.Error("modification.js must send the playback beacon")
	}
	if !strings.Contains(body, `"lampacid0123456789"`) || strings.Count(body, testToken) != 1 {
		t.Errorf("Lampac must get its own household id, never the gateway token:\n%s", body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw:8080/h/"+testToken+"/lampainit.js?v=1", nil))
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `window.lampa_settings = {`) ||
		!strings.Contains(body, `"http://gw:8080/h/`+testToken+`/plugins/modification.js"`) {
		t.Errorf("lampainit.js = %d:\n%s", rec.Code, body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw:8080/h/"+testToken+"/", nil))
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.HasPrefix(csp, "script-src 'self'") || !strings.Contains(csp, "http://gw:9118") {
		t.Errorf("index.html CSP = %q", csp)
	}
}

// A fake TorrServer starts with upstream defaults: the first reconcile writes once and keeps keys the gateway
// does not manage; the second finds no drift and writes nothing (a write would drop active torrents).
func TestReconcileSettings(t *testing.T) {
	live := map[string]any{"CacheSize": 64 << 20, "DisableUpload": false, "UploadRateLimit": 0,
		"ConnectionsLimit": 25, "EnableLPD": true, "FriendlyName": "keep me", "DefaultTrackers": "udp://x"}
	sets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "gw" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Action string
			Sets   map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Action == "set" {
			sets++
			live = req.Sets
			return
		}
		_ = json.NewEncoder(w).Encode(live)
	}))
	defer srv.Close()
	ts := &torrServer{base: srv.URL, user: "gw", pass: "pw"}
	want := wantSettings(512, 0)
	for i := 0; i < 2; i++ {
		if err := ts.reconcile(t.Context(), want); err != nil {
			t.Fatal(err)
		}
	}
	if sets != 1 {
		t.Fatalf("set called %d times, want 1", sets)
	}
	if d := drift(live, want); len(d) != 0 {
		t.Fatalf("still differing: %v", d)
	}
	if live["FriendlyName"] != "keep me" || live["DefaultTrackers"] != "udp://x" {
		t.Fatalf("unmanaged keys lost: %v", live)
	}
	if live["DisableUpload"] != true {
		t.Fatal("upload not disabled")
	}
	if !ts.ready.Load() {
		t.Fatal("not marked ready after the settings were applied")
	}
	want = wantSettings(512, 125)
	if d := drift(map[string]any{"DisableUpload": false, "UploadRateLimit": 125}, want); len(d) != len(want)-2 {
		t.Fatalf("capped upload: %d differing keys, want all but the two upload keys", len(d))
	}
}

// Three indexers: fast, slow and broken. The first search answers at the deadline with the fast one's release;
// the slow one keeps running, and the same search later returns both without asking the finished ones again.
func TestFanoutPartialThenComplete(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"fast","enable":true,"protocol":"torrent"},
				{"id":2,"name":"slow","enable":true,"protocol":"torrent"},
				{"id":3,"name":"broken","enable":true,"protocol":"torrent"},
				{"id":4,"name":"off","enable":false,"protocol":"torrent"}]`))
			return
		}
		id := r.URL.Query().Get("indexerIds")
		mu.Lock()
		calls[id]++
		mu.Unlock()
		switch id {
		case "1":
			_, _ = w.Write([]byte(`[{"title":"Fast 1080p","indexer":"fast","protocol":"torrent","magnetUrl":"magnet:?xt=urn:btih:a"}]`))
		case "2":
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`[{"title":"Slow 2160p","indexer":"slow","protocol":"torrent","magnetUrl":"magnet:?xt=urn:btih:b"}]`))
		case "3":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("disabled or unknown indexer %q asked", id)
		}
	}))
	defer fake.Close()
	f := newFanout(newProwlarr(fake.URL, "k"))
	f.deadline = 50 * time.Millisecond
	first, partial, err := f.search(t.Context(), "x", "search", nil, nil)
	if err != nil || !partial || len(first) != 1 || first[0].Title != "Fast 1080p" {
		t.Fatalf("first search: %v partial=%v %+v", err, partial, first)
	}
	time.Sleep(500 * time.Millisecond)
	second, partial, err := f.search(t.Context(), "x", "search", nil, nil)
	if err != nil || partial || len(second) != 2 {
		t.Fatalf("second search: %v partial=%v %+v", err, partial, second)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["1"] != 1 || calls["2"] != 1 || calls["3"] != 1 {
		t.Fatalf("each indexer must be asked once, got %v", calls)
	}
}

// Searches beyond maxRunning cancel the oldest one still running; what it found so far stays usable.
func TestFanoutCancelsOldSearches(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"fast","enable":true},{"id":2,"name":"slow","enable":true}]`))
			return
		}
		if r.URL.Query().Get("indexerIds") == "2" {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
			return
		}
		_, _ = w.Write([]byte(`[{"title":"` + r.URL.Query().Get("query") + `","protocol":"torrent","magnetUrl":"magnet:?x"}]`))
	}))
	defer fake.Close()
	f := newFanout(newProwlarr(fake.URL, "k"))
	f.deadline, f.maxRunning = 50*time.Millisecond, 1
	if _, partial, _ := f.search(t.Context(), "a", "search", nil, nil); !partial {
		t.Fatal("first search should still be running")
	}
	first := f.cached("search\x00a\x00")
	if _, _, err := f.search(t.Context(), "b", "search", nil, nil); err != nil {
		t.Fatal(err)
	}
	if first == nil || first.ctx.Err() == nil {
		t.Fatal("the older running search was not cancelled")
	}
	time.Sleep(200 * time.Millisecond)
	if again, partial, _ := f.search(t.Context(), "a", "search", nil, nil); partial || len(again) != 1 {
		t.Fatalf("cancelled search: partial=%v %d releases, want its 1 release and no pending work", partial, len(again))
	}
}

// The parser corpus: release titles with the reviewed expected parse, kept outside the public repository.
// Every field of every title counts; below 95 % correct fails the build, and each miss is printed.
func TestCorpus(t *testing.T) {
	b, err := os.ReadFile("testdata/corpus.jsonl")
	if os.IsNotExist(err) {
		t.Skip("no parser corpus in this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	fields, wrong := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c struct {
			Title string
			Want  map[string]any
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		raw, _ := json.Marshal(defaultVocab.parse(c.Title))
		_ = json.Unmarshal(raw, &got)
		for _, k := range []string{"resolution", "source", "codec", "hdr", "dv", "threed", "voices", "languages",
			"subs", "original", "studios", "seasons", "year", "junk"} {
			fields++
			w, _ := json.Marshal(c.Want[k])
			g, _ := json.Marshal(got[k])
			if k == "studios" { // a set: order does not matter
				w, g = []byte(sortedJSON(c.Want[k])), []byte(sortedJSON(got[k]))
			}
			if string(w) != string(g) {
				wrong++
				t.Logf("%s: %s, want %s\n    %s", k, g, w, c.Title)
			}
		}
	}
	acc := 1 - float64(wrong)/float64(fields)
	t.Logf("corpus: %d fields, %d wrong, %.1f %% correct", fields, wrong, 100*acc)
	if acc < 0.95 {
		t.Fatalf("corpus accuracy %.1f %% is below the 95 %% target", 100*acc)
	}
}

func sortedJSON(v any) string {
	list, _ := v.([]any)
	var s []string
	for _, x := range list {
		s = append(s, fmt.Sprint(x))
	}
	sort.Strings(s)
	b, _ := json.Marshal(s)
	return string(b)
}

// Card matching, the seeder floor and ranking, with made-up titles.
func TestMatchAndRank(t *testing.T) {
	rel := func(title string, seeders int) prowlarrRelease {
		return prowlarrRelease{Title: title, Protocol: "torrent", Seeders: intp(seeders), MagnetURL: "magnet:?" + title}
	}
	in := []prowlarrRelease{
		rel("Нова / Nova (2021) CAMRip | P", 100),
		rel("Нова / Nova: Part One (2021) BDRip 1080p | D", 50),
		rel("Нова / Nova (1984) BDRip 1080p | D", 50),
		rel("Nova.Part.Two.2024.2160p.WEB-DL.DV.HDR.H265", 80),
		rel("OST - Нова / Nova (2021) FLAC", 40),
		rel("Новакерк / Novakirk (2017) BDRip 1080p", 60),
		rel("Нова / Nova (2021) WEB-DL 720p", 1),
	}
	q, _ := url.ParseQuery("Query=Nova%202021&title=%D0%9D%D0%BE%D0%B2%D0%B0&title_original=Nova&year=2021&is_serial=1")
	out := toJackett(in, newLinkStore(time.Hour, 100), "http://gw/h/t", cardFrom(q), 3, 50)
	var titles []string
	for _, r := range out {
		titles = append(titles, r.Title)
	}
	want := []string{"Нова / Nova: Part One (2021) BDRip 1080p | D", "Нова / Nova (2021) CAMRip | P"}
	if strings.Join(titles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(titles, "\n"), strings.Join(want, "\n"))
	}
	if out[0].Rank <= out[1].Rank {
		t.Fatalf("ranks %d, %d: the BDRip must outrank the camera copy", out[0].Rank, out[1].Rank)
	}
	// A series: Lampa sends title=undefined; later seasons carry later years and must stay.
	q, _ = url.ParseQuery("Query=%D0%A2%D0%B8%D1%85%D0%B8%D0%B9%20%D0%B3%D0%BE%D1%80%D0%BE%D0%B4&title=undefined&title_original=undefined&year=2008&is_serial=2")
	out = toJackett([]prowlarrRelease{rel("Тихий город / Quiet Town [S04] (2011) WEB-DL 1080p", 20)},
		newLinkStore(time.Hour, 10), "http://gw/h/t", cardFrom(q), 3, 50)
	if len(out) != 1 {
		t.Fatalf("a later season of the series was dropped: %+v", out)
	}
}

func TestStatus(t *testing.T) {
	state := "running"
	glu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/vpn/status":
			fmt.Fprintf(w, `{"status":%q}`, state)
		case "/v1/publicip/ip":
			fmt.Fprint(w, `{"public_ip":"203.0.113.7","country":"Netherlands","city":"Amsterdam"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer glu.Close()
	get := func(control string) string {
		g := &gateway{cfg: config{Token: testToken, VPNControlURL: control}, ts: &torrServer{}}
		g.ts.ready.Store(true)
		rec := httptest.NewRecorder()
		g.publicHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/"+testToken+"/status", nil))
		return strings.TrimSpace(rec.Body.String())
	}
	for _, c := range []struct{ control, state, want string }{
		{"", "running", `{"torrserver":true,"vpn":null}`},
		{glu.URL, "running", `{"torrserver":true,"vpn":{"running":true,"ip":"203.0.113.7","country":"Netherlands","city":"Amsterdam"}}`},
		{glu.URL, "stopped", `{"torrserver":true,"vpn":{"running":false}}`},
		{"http://127.0.0.1:1", "running", `{"torrserver":true,"vpn":{"running":false}}`}, // control server unreachable
	} {
		state = c.state
		if got := get(c.control); got != c.want {
			t.Errorf("control=%q state=%s: got %s, want %s", c.control, c.state, got, c.want)
		}
	}
}

// The early answer counts only releases the caller can use: a burst for other titles must not end the wait.
func TestFanoutSoftCountsKeptOnly(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"fast","enable":true,"protocol":"torrent"},
				{"id":2,"name":"slow","enable":true,"protocol":"torrent"}]`))
			return
		}
		if r.URL.Query().Get("indexerIds") == "2" {
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`[{"title":"Wanted 1080p","protocol":"torrent","magnetUrl":"magnet:?xt=urn:btih:b"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"title":"Other 1080p","protocol":"torrent","magnetUrl":"magnet:?xt=urn:btih:a"}]`))
	}))
	defer fake.Close()
	f := newFanout(newProwlarr(fake.URL, "k"))
	f.enough, f.soft, f.deadline = 1, 10*time.Millisecond, 5*time.Second
	if got, partial, _ := f.search(t.Context(), "a", "search", nil, nil); !partial || len(got) != 1 {
		t.Fatalf("without a filter the first release ends the wait: partial=%v %+v", partial, got)
	}
	got, _, _ := f.search(t.Context(), "b", "search", nil, func(r prowlarrRelease) bool { return r.Title == "Wanted 1080p" })
	if len(got) != 2 {
		t.Fatalf("answered before a kept release arrived: %+v", got)
	}
	if strings.Join(tokens("Shōgun / Amélie"), " ") != "shogun amelie" {
		t.Fatalf("diacritics not folded: %v", tokens("Shōgun / Amélie"))
	}
}

func TestPrefs(t *testing.T) {
	if p := prefsFrom("l=uk,r=2160,h=0,d=android"); p != (prefs{lang: "uk", res: 2160, noHDR: true, device: "android"}) {
		t.Fatalf("parsed %+v", p)
	}
	if p := prefsFrom("l=xx,r=999,d=Bad Device!,junk"); p != (prefs{}) {
		t.Fatalf("bad values must fall back to defaults: %+v", p)
	}
	if !isPrefsSearch("jackett/p/l=uk/api/v2.0/indexers/all/results") || isPrefsSearch("jackett/p/l=uk/ts/x") {
		t.Fatal("prefs search route")
	}
	ru := defaultVocab.parse("Фильм / Film (2020) WEB-DL 1080p | Дубляж")
	uk := defaultVocab.parse("Фільм / Film (2020) WEB-DL 1080p | UKR | Дубляж")
	if score(ru, 10, prefs{}) < score(uk, 10, prefs{}) || score(uk, 10, prefs{lang: "uk"}) <= score(ru, 10, prefs{lang: "uk"}) {
		t.Fatal("first audio language must lead")
	}
	uhd := defaultVocab.parse("Film (2020) WEB-DL 2160p HDR")
	fhd := defaultVocab.parse("Film (2020) WEB-DL 1080p")
	if score(uhd, 10, prefs{res: 2160}) <= score(fhd, 10, prefs{res: 2160}) || score(uhd, 10, prefs{}) >= score(fhd, 10, prefs{}) {
		t.Fatal("preferred resolution must lead")
	}
	if score(uhd, 26, prefs{res: 2160}) <= score(fhd, 1962, prefs{res: 2160}) { // seen on the Pi with a real 2160p release
		t.Fatal("an explicit 4K choice must beat a far better seeded 1080p")
	}
	if score(uhd, 10, prefs{res: 2160, noHDR: true}) >= score(fhd, 10, prefs{res: 2160, noHDR: true}) {
		t.Fatal("an SDR screen must sink HDR")
	}
	av1 := defaultVocab.parse("Film (2020) WEB-DL 1080p AV1")
	if score(av1, 10, prefs{device: "android"}) >= score(av1, 10, prefs{device: "browser"}) {
		t.Fatal("the device table must apply")
	}
}

func TestTMDBProxy(t *testing.T) {
	var calls int
	var lastKey, lastAuth string
	throttle := false
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		lastKey, lastAuth = r.URL.Query().Get("api_key"), r.Header.Get("Authorization")
		if throttle {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch r.URL.Query().Get("language") {
		case "uk":
			fmt.Fprint(w, `{"id":603,"title":"Матриця","overview":""}`)
		case "ru":
			fmt.Fprint(w, `{"id":603,"title":"Матрица","overview":"Хакер Нео узнаёт правду."}`)
		default:
			fmt.Fprint(w, `{"id":603,"title":"The Matrix","overview":"Neo learns the truth."}`)
		}
	}))
	defer fake.Close()
	g := &gateway{cfg: config{Token: testToken, LampacPort: "9118", LampacID: "lampacid0123456789"}, ts: &torrServer{}, tmdb: newTMDB("household-key")}
	g.tmdb.upstream = fake.URL
	h := g.publicHandler()
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw:8080"+path, nil))
		return rec.Code, rec.Body.String()
	}
	movie := "/h/" + testToken + "/tmdb/http://api.themoviedb.org/3/movie/603?language=uk&api_key=lampas-key&email=x"
	code, body := get(movie)
	if code != 200 || !strings.Contains(body, "Хакер Нео") || !strings.Contains(body, "Матриця") {
		t.Fatalf("empty Ukrainian overview must be filled from Russian, keeping the Ukrainian title: %d %s", code, body)
	}
	if lastKey != "household-key" {
		t.Fatalf("upstream got api_key %q, want the household's", lastKey)
	}
	before := calls
	if code, _ = get(movie); code != 200 || calls != before {
		t.Fatalf("second request must be a cache hit: %d calls %d→%d", code, before, calls)
	}
	for _, bad := range []string{
		"/h/wrong-token-wrong-token-wrong-tok/tmdb/http://api.themoviedb.org/3/movie/603",
		"/h/" + testToken + "/tmdb/http://evil.example/3/movie/603",
		"/h/" + testToken + "/tmdb/http://api.themoviedb.org/4/list/1",
	} {
		if code, _ := get(bad); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", bad, code)
		}
	}
	throttle = true
	if code, body = get(movie); code != 200 { // fresh in cache: no upstream call at all
		t.Fatalf("cached answer during throttling: %d", code)
	}
	if code, _ = get("/h/" + testToken + "/tmdb/https://api.themoviedb.org/3/trending/all/day?language=ru"); code != http.StatusServiceUnavailable {
		t.Fatalf("429 without a cached answer: %d, want 503", code)
	}
	before = calls
	if code, _ = get("/h/" + testToken + "/tmdb/https://api.themoviedb.org/3/trending/all/week?language=ru"); code != http.StatusServiceUnavailable || calls != before {
		t.Fatalf("while blocked TMDB must not be called: %d, calls %d→%d", code, before, calls)
	}
	g.tmdb = newTMDB("eyJhbGciOiJIUzI1NiJ9.token")
	g.tmdb.upstream = fake.URL
	throttle = false
	h = g.publicHandler()
	if code, _ = get("/h/" + testToken + "/tmdb/https://api.themoviedb.org/3/movie/1?language=en"); code != 200 || lastAuth != "Bearer eyJhbGciOiJIUzI1NiJ9.token" || lastKey != "" {
		t.Fatalf("a read access token goes in the Authorization header: %d %q %q", code, lastAuth, lastKey)
	}
	_, js := get("/h/" + testToken + "/plugins/modification.js")
	if !strings.Contains(js, "var tmdbProxy = true;") {
		t.Fatal("plugin must switch Lampa to the TMDB proxy when the server has a key")
	}
	if csp := contentSecurityPolicy("http://gw:9118", true); strings.Contains(csp, "api.themoviedb.org") {
		t.Fatalf("with the proxy the browser must not reach TMDB's API: %s", csp)
	}
}

func TestPlatformsServed(t *testing.T) {
	g := &gateway{cfg: config{Token: testToken, LampacPort: "9118", LampacID: "lampacid0123456789"}, ts: &torrServer{}}
	h := g.publicHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/"+testToken+"/platforms.json", nil))
	var list []struct{ Name, Match string }
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) < 5 {
		t.Fatalf("platforms.json: %v %d entries", err, len(list))
	}
	for _, p := range list {
		if _, err := regexp.Compile(p.Match); err != nil || p.Name == "" {
			t.Errorf("platform %+v: %v", p, err)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/"+testToken+"/plugins/platforms.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Lampa.Api.sources.ms_platforms") {
		t.Fatalf("platforms.js: %d", rec.Code)
	}
}

func TestProfileStore(t *testing.T) {
	dir := t.TempDir()
	g := &gateway{cfg: config{Token: testToken}, ts: &torrServer{}, prof: newProfileStore(dir)}
	h := g.publicHandler()
	call := func(method, body string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "http://gw/h/"+testToken+"/profiles", strings.NewReader(body)))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := call(http.MethodGet, ""); code != 200 || body != `[{"id":"","name":""}]` {
		t.Fatalf("a new household has only the main profile: %d %s", code, body)
	}
	call(http.MethodPost, `{"action":"add","name":" Аня "}`)
	call(http.MethodPost, `{"action":"add","name":"Дети"}`)
	if _, body := call(http.MethodPost, `{"action":"remove","id":"2"}`); body != `[{"id":"","name":""},{"id":"3","name":"Дети"}]` {
		t.Fatalf("after remove: %s", body)
	}
	if _, body := call(http.MethodPost, `{"action":"add","name":"Гость"}`); !strings.Contains(body, `{"id":"4","name":"Гость"}`) {
		t.Fatalf("ids are never reused: %s", body)
	}
	for _, bad := range []string{`{"action":"remove","id":""}`, `{"action":"add","name":"   "}`,
		`{"action":"add","name":"` + strings.Repeat("я", 25) + `"}`, `{"action":"drop"}`, `not json`} {
		if code, _ := call(http.MethodPost, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
	if newProfileStore(dir).data.Next != 5 || len(newProfileStore(dir).data.Profiles) != 3 {
		t.Fatal("profiles must survive a restart")
	}
}

func TestOnlineFlag(t *testing.T) {
	for _, on := range []bool{false, true} {
		g := &gateway{cfg: config{Token: testToken, LampacPort: "9118", LampacID: "lampacid0123456789", Online: on}, ts: &torrServer{}}
		rec := httptest.NewRecorder()
		g.publicHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/"+testToken+"/plugins/modification.js", nil))
		if want := "var online = " + strconv.FormatBool(on) + ";"; !strings.Contains(rec.Body.String(), want) {
			t.Errorf("online=%v: plugin lacks %q", on, want)
		}
	}
}

// Swarm metrics come from TorrServer's list at scrape time; uploaded piece data is a counter that survives a
// torrent being dropped, and each upstream gets an up value.
func TestProbeCollector(t *testing.T) {
	list := `[{"hash":"a","download_speed":1000,"total_peers":9,"active_peers":4,"connected_seeders":3,"bytes_written_data":0},
		{"hash":"b","download_speed":500,"total_peers":1,"active_peers":1,"bytes_written_data":100}]`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); r.URL.Path != "/torrents" || u != "u" || p != "p" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, list)
	}))
	defer ts.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer ok.Close()

	g := &gateway{cfg: config{TorrServerURL: ts.URL, TorrServerUser: "u", TorrServerPass: "p",
		ProwlarrURL: ok.URL, LampacURL: "http://127.0.0.1:1"}}
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(newProbeCollector(g))
	scrape := func() map[string]float64 {
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, mf := range mfs {
			for _, m := range mf.GetMetric() {
				key := mf.GetName()
				for _, l := range m.GetLabel() {
					key += "/" + l.GetValue()
				}
				out[key] = m.GetGauge().GetValue() + m.GetCounter().GetValue()
			}
		}
		return out
	}
	got := scrape()
	for k, want := range map[string]float64{
		"gateway_upstream_up/prowlarr": 1, "gateway_upstream_up/torrserver": 1, "gateway_upstream_up/lampac": 0,
		"gateway_torrents_active": 2, "gateway_torrent_peers/total": 10, "gateway_torrent_peers/active": 5,
		"gateway_torrent_peers/seeders": 3, "gateway_torrent_download_bytes_per_second": 1500,
		"gateway_torrent_upload_bytes_total": 100,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if _, ok := got["gateway_upstream_up/vpn"]; ok {
		t.Error("vpn probed without a VPN configured")
	}
	// b dropped, a uploads 50, then b comes back with a fresh total of 30: the counter only grows.
	list = `[{"hash":"a","bytes_written_data":50}]`
	if v := scrape()["gateway_torrent_upload_bytes_total"]; v != 150 {
		t.Errorf("after drop: %v, want 150", v)
	}
	list = `[{"hash":"a","bytes_written_data":50},{"hash":"b","bytes_written_data":30}]`
	if v := scrape()["gateway_torrent_upload_bytes_total"]; v != 180 {
		t.Errorf("after re-add: %v, want 180", v)
	}
}

// The player's stream gets a TTFB observation and counts as active while it runs; a preload does not.
func TestStreamTTFB(t *testing.T) {
	before := testutilCount(t)
	h := observe("ts_stream", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) })
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gw/stream/f?link=x&index=1&preload", nil))
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gw/stream/f?link=x&index=1&play", nil))
	if got := testutilCount(t) - before; got != 1 {
		t.Errorf("TTFB observations = %d, want 1", got)
	}
}

func testutilCount(t *testing.T) uint64 {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "gateway_stream_ttfb_seconds" {
			return mf.GetMetric()[0].GetHistogram().GetSampleCount()
		}
	}
	return 0
}

// Lampa drops its &preload request after 2 s while TorrServer still holds it: a client abort, logged as 499
// without a warning, not as TorrServer failing (502).
func TestTorrServerClientAbort(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a preload that is not finished yet
	}))
	defer upstream.Close()
	ts := &torrServer{base: upstream.URL}
	ts.ready.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hash := strings.Repeat("a", 40)
	req := httptest.NewRequest(http.MethodGet, "http://gw/h/x/ts/stream/e.mkv?link="+hash+"&index=1&preload", nil)
	rec := httptest.NewRecorder()
	ts.handle("/stream/e.mkv")(rec, req.WithContext(ctx))
	if rec.Code != 499 || strings.Contains(logs.String(), "torrserver proxy") {
		t.Fatalf("client abort = %d, log %q; want 499 and no warning", rec.Code, logs.String())
	}

	upstream.Close() // TorrServer itself gone: still 502 with a warning
	rec = httptest.NewRecorder()
	ts.handle("/stream/e.mkv")(rec, httptest.NewRequest(http.MethodGet, "http://gw/h/x/ts/stream/e.mkv?link="+hash+"&index=1&play", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(logs.String(), "torrserver proxy") {
		t.Fatalf("TorrServer down = %d, log %q; want 502 and a warning", rec.Code, logs.String())
	}
}

// The beacon maps client words onto fixed label sets and refuses what it cannot count.
func TestBeacon(t *testing.T) {
	post := func(method, body string) int {
		rec := httptest.NewRecorder()
		handleBeacon(rec, httptest.NewRequest(method, "http://gw/h/x/beacon", strings.NewReader(body)))
		return rec.Code
	}
	for body, want := range map[string]int{
		`{"e":"ttff","s":3.5,"p":"tizen","src":"torrent"}`:    http.StatusNoContent,
		`{"e":"ttff","s":2,"p":"a-made-up-tv","src":"weird"}`: http.StatusNoContent,
		`{"e":"rebuffer","p":"browser","src":"online"}`:       http.StatusNoContent,
		`{"e":"error","p":"webos","src":"torrent"}`:           http.StatusNoContent,
		`{"e":"ttff","s":0,"p":"tizen","src":"torrent"}`:      http.StatusBadRequest,
		`{"e":"ttff","s":3600,"p":"tizen","src":"torrent"}`:   http.StatusBadRequest,
		`{"e":"delete-everything"}`:                           http.StatusBadRequest,
		`not json`:                                            http.StatusBadRequest,
	} {
		if got := post(http.MethodPost, body); got != want {
			t.Errorf("POST %s = %d, want %d", body, got, want)
		}
	}
	if got := post(http.MethodGet, ""); got != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", got)
	}
	series := map[string]string{}
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), "client_") {
			continue
		}
		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			for _, l := range m.GetLabel() {
				key += "," + l.GetValue()
			}
			series[key] = fmt.Sprint(m.GetHistogram().GetSampleCount() + uint64(m.GetCounter().GetValue()))
		}
	}
	want := map[string]string{"client_ttff_seconds,tizen,torrent": "1", "client_ttff_seconds,other,other": "1",
		"client_rebuffer_total,browser,online": "1", "client_playback_errors_total,webos,torrent": "1"}
	if fmt.Sprint(series) != fmt.Sprint(want) {
		t.Errorf("client series = %v, want %v", series, want)
	}
}
