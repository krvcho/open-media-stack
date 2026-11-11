"""Lampa canary: our plugin against the pinned build and against today's upstream build.

Needs the stack running with the test seeder and its Prowlarr indexer (run test/leak/leak_test.py first), and
Playwright with a local Chrome:

    pip install playwright==1.62.0
    python3 test/canary/canary_test.py

Two runs in headless Chrome:
  pinned    the gateway's own page and pinned Lampa build, as browsers and the Android app get it;
  upstream  today's https://yumata.github.io/lampa/ (page, app and assets) served under the gateway's origin,
            with our plugin added the way a TV user adds it (Settings -> Extensions). Same origin, so no mixed
            content or private-network blocking stands between the upstream page and the gateway.
Each run checks that the plugin loaded without errors, wrote its settings, loaded Lampac's sync scripts, and
that a search made by Lampa's own parser code reaches the gateway and returns the seeder release. Never prints
a secret.
"""
import json
import os
import sys
import urllib.parse

from playwright.sync_api import sync_playwright

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
UPSTREAM = "https://yumata.github.io/lampa/"


def read_env():
    env = {}
    with open(os.path.join(ROOT, ".env"), encoding="utf8") as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                env[k] = v
    return env


ENV = read_env()
TOKEN = ENV["HOUSEHOLD_TOKEN"]
BASE = f"http://localhost:{ENV.get('GATEWAY_PORT', '8080')}/h/{TOKEN}"
LAMPAC_PORT = ENV.get("LAMPAC_PORT", "9118")
OURS = ("plugins/modification.js", "plugins/platforms.js", "platforms.json", "lampainit.js", "status")  # stay ours upstream
failures = []


def check(run, name, ok, detail=""):
    print(("PASS" if ok else "FAIL"), run, name, ("- " + detail.replace(TOKEN, "<token>")) if detail and not ok else "")
    if not ok:
        failures.append(f"{run}: {name}")


def upstream_route(context):
    """Serve every Lampa file under the gateway origin from upstream, except our plugin and the API routes."""
    prefix = BASE + "/"

    def handle(route):
        rest = route.request.url[len(prefix):]
        path = urllib.parse.urlsplit(rest).path
        if path in OURS or path.split("/")[0] in ("jackett", "ts", "t"):
            return route.continue_()
        resp = context.request.get(UPSTREAM + rest, timeout=60_000)
        route.fulfill(response=resp)

    context.route(prefix + "**", handle)


SEARCH_JS = """() => new Promise((resolve) => {
  const movie = {title: 'Media Stack Test Clip', original_title: 'Media Stack Test Clip', release_date: '2026-01-01',
                 genres: [], original_language: 'en', number_of_seasons: 0};
  Lampa.Parser.get({search: 'Media Stack Test Clip', movie: movie, other: false},
    (data) => resolve({ok: true, results: (data.Results || []).map(r => ({tracker: r.Tracker, rank: r.Rank}))}),
    (e) => resolve({ok: false, error: String(e && (e.statusText || e.status || e))}));
})"""

STATE_JS = """() => ({
  version: Lampa.Manifest && Lampa.Manifest.app_digital,
  parser: Lampa.Storage.get('parser_torrent_type'),
  jackett: Lampa.Storage.get('jackett_url'),
  torrserver: Lampa.Storage.get('torrserver_url'),
  client: String(Lampa.Storage.get('internal_torrclient')),
  sort: Lampa.Storage.get('torrents_sort'),
})"""


def run(pw, name):
    # Chrome treats a page served through route.fulfill as public, and its Local Network Access check then
    # refuses the page's requests to Lampac on loopback. A TV's page is a local file, so turn the check off for
    # the upstream run only.
    args = ["--disable-features=LocalNetworkAccessChecks"] if name == "upstream" else []
    browser = pw.chromium.launch(channel="chrome", headless=True, args=args)
    context = browser.new_context()
    errors, sync = [], {}
    # A device that has been used once: Lampa starts the app and its plugins only after the first-run language
    # choice (lampa-source src/app.js:889).
    context.add_init_script("try { localStorage.setItem('language', 'ru') } catch (e) {}")
    if name == "upstream":
        upstream_route(context)
        # The TV path: the plugin URL as Settings -> Extensions stores it.
        plugin = json.dumps([{"url": BASE + "/plugins/modification.js", "status": 1, "name": "media-stack"}])
        context.add_init_script(f"try {{ localStorage.setItem('plugins', {json.dumps(plugin)}) }} catch (e) {{}}")
    page = context.new_page()
    page.on("pageerror", lambda e: errors.append(f"{e.message} {e.stack or ''}"))
    page.on("response", lambda r: sync.__setitem__(r.url.split("/")[3], r.status)
            if f":{LAMPAC_PORT}/" in r.url and "/js/" in r.url else None)
    try:
        page.goto(BASE + "/", timeout=120_000)
        page.wait_for_function("() => window.Lampa && Lampa.Storage && Lampa.Parser", timeout=120_000)
        check(name, "Lampa started", True)
        try:
            page.wait_for_function("() => window.media_stack_ready === true", timeout=60_000)
            ready = True
        except Exception:
            ready = False
        check(name, "plugin loaded", ready)
        state = page.evaluate(STATE_JS)
        print("    Lampa", state.get("version"))
        check(name, "settings written",
              state["parser"] == "jackett" and state["jackett"].startswith(BASE + "/jackett/p/l=ru,r=1080,h=1,d=") and
              state["torrserver"] == BASE + "/ts" and state["client"] == "true" and state["sort"] == "Rank",
              json.dumps(state))
        page.wait_for_timeout(5_000)  # the sync scripts load after the plugin
        check(name, "Lampac sync scripts loaded", sync.get("timecode") == 200 and sync.get("bookmark") == 200,
              json.dumps(sync))
        try:  # the Platforms plugin loads after the project's plugin
            page.wait_for_function("() => window.media_stack_platforms === true && !!Lampa.Api.sources.ms_platforms",
                                   timeout=30_000)
            platforms = True
        except Exception:
            platforms = False
        check(name, "Platforms plugin loaded", platforms)
        found = page.evaluate(SEARCH_JS)
        seeded = [r for r in found.get("results", []) if r["tracker"] == "Local test seeder"]
        check(name, "Lampa's search reaches the gateway", found.get("ok") and bool(seeded) and "rank" in seeded[0],
              json.dumps(found)[:300])
    except Exception as e:  # a hard failure (Lampa never started) is one failure, not a crash
        check(name, "page runs", False, str(e)[:300])
    ours = [e for e in errors if any(p in e for p in OURS)]
    check(name, "no errors from our scripts", not ours, " | ".join(ours)[:500])
    if errors and not ours:
        print(f"    note: {len(errors)} uncaught error(s) from Lampa itself, not ours")
    browser.close()


def main():
    with sync_playwright() as pw:
        for name in sys.argv[1:] or ("pinned", "upstream"):
            run(pw, name)
    if failures:
        print(f"\n{len(failures)} failed: " + "; ".join(failures))
        sys.exit(1)
    print("\nall checks passed")


if __name__ == "__main__":
    main()
