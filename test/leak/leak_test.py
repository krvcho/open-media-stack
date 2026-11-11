"""End-to-end leak and perimeter test.

Needs the stack running with the test seeder:

    docker compose --profile test up -d --build

Reads secrets from .env, drives search -> add -> stream through the gateway, then checks that no upstream
secret appears in any client response, container log or TorrServer's stored data, and that the perimeter
refuses what it should. Exits non-zero on the first failed group. Never prints a secret.
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


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
GW = f"http://localhost:{ENV.get('GATEWAY_PORT', '8080')}"
BASE = f"{GW}/h/{ENV['HOUSEHOLD_TOKEN']}"
LAMPAC = f"http://localhost:{ENV.get('LAMPAC_PORT', '9118')}"
# Prowlarr listens on PROWLARR_BIND only (a LAN address on a real server, see .env.example).
PROWLARR = f"http://{ENV.get('PROWLARR_BIND', '127.0.0.1')}:{ENV.get('PROWLARR_PORT', '9696')}"
# Secrets that must never reach a client. The household token is in client URLs by design.
CLIENT_SECRETS = {k: ENV[k] for k in ("PROWLARR_API_KEY", "TORRSERVER_PASSWORD", "TMDB_API_KEY") if ENV.get(k)}
# Secrets that must never appear in logs or in TorrServer's stored data.
STORED_SECRETS = {k: ENV[k] for k in ("PROWLARR_API_KEY", "TORRSERVER_PASSWORD", "HOUSEHOLD_TOKEN", "LAMPAC_HOUSEHOLD_ID", "TMDB_API_KEY") if ENV.get(k)}

client_bodies = []
failures = []


def http(method, url, body=None, headers=None, limit=None):
    """Return (status, bytes). Records every gateway/Lampac response body for the leak scan."""
    data = body.encode() if isinstance(body, str) else body
    req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            status, payload = r.status, r.read(limit) if limit else r.read()
    except urllib.error.HTTPError as e:
        status, payload = e.code, e.read()
    if url.startswith(GW) or url.startswith(LAMPAC):
        client_bodies.append(payload)
    return status, payload


def check(name, ok):
    print(("PASS " if ok else "FAIL ") + name)
    if not ok:
        failures.append(name)


def compose(*args):
    return subprocess.run(["docker", "compose", *args], cwd=ROOT, capture_output=True, text=True,
                          encoding="utf8", errors="replace").stdout


def wait_ready():
    checks = {
        "gateway": lambda: http("GET", GW + "/healthz")[0] == 200,
        "prowlarr": lambda: http("GET", PROWLARR + "/api/v1/indexer",
                                 headers={"X-Api-Key": ENV["PROWLARR_API_KEY"]})[0] == 200,
        "lampac": lambda: http("GET", LAMPAC + "/timecode/js/x")[0] == 200,
    }
    pending = list(checks)
    deadline = time.time() + 180
    while time.time() < deadline:
        for name in list(pending):
            try:
                if checks[name]():
                    pending.remove(name)
            except (urllib.error.URLError, ConnectionError):
                pass
        if not pending:
            return
        time.sleep(3)
    sys.exit(f"stack not ready after 180 s: {', '.join(pending)}")


def ensure_seeder_indexer():
    key = {"X-Api-Key": ENV["PROWLARR_API_KEY"], "Content-Type": "application/json"}
    _, body = http("GET", PROWLARR + "/api/v1/indexer", headers=key)
    for ix in json.loads(body):
        if any(f.get("name") == "baseUrl" and f.get("value") == "http://seeder:9117" for f in ix["fields"]):
            return
    _, body = http("GET", PROWLARR + "/api/v1/indexer/schema", headers=key)
    schema = next(s for s in json.loads(body) if s.get("name") == "Generic Torznab")
    schema.update(enable=True, appProfileId=1, name="Local test seeder")
    for f in schema["fields"]:
        if f["name"] == "baseUrl":
            f["value"] = "http://seeder:9117"
        if f["name"] == "apiPath":
            f["value"] = "/api"
    status, _ = http("POST", PROWLARR + "/api/v1/indexer", json.dumps(schema), key)
    if status != 201:
        sys.exit(f"could not add the test seeder indexer: HTTP {status}")


def main():
    wait_ready()
    ensure_seeder_indexer()

    # Perimeter: wrong token, blocked TorrServer routes, foreign links.
    wrong = f"{GW}/h/{'0' * 64}"
    for path in ("/", "/jackett/api/v2.0/indexers/all/results?Query=x", "/ts/echo", "/t/" + "a" * 32):
        check(f"wrong token -> 404 on {path.split('?')[0]}", http("GET", wrong + path)[0] == 404)
    check("/ts/shutdown refused", http("GET", BASE + "/ts/shutdown")[0] == 404)
    check("TorrServer settings write refused",
          http("POST", BASE + "/ts/settings", '{"action":"set","sets":{}}')[0] == 400)
    check("foreign link refused",
          http("POST", BASE + "/ts/torrents", '{"action":"add","link":"http://prowlarr:9696/x"}')[0] == 400)
    check("unknown release id -> 404",
          http("POST", BASE + "/ts/torrents", '{"action":"add","link":"' + BASE + "/t/" + "b" * 32 + '"}')[0] == 404)

    # Lampac serves sync only with the household id.
    _, anon = http("GET", LAMPAC + "/bookmark/list")
    check("Lampac refuses sync without the household id", b'"accsdb":true' in anon)
    _, own = http("GET", LAMPAC + "/bookmark/list?token=" + ENV["LAMPAC_HOUSEHOLD_ID"])
    check("Lampac serves sync with the household id", b'"accsdb":true' not in own)

    # Happy path: search -> add -> stream.
    status, body = http("GET", BASE + "/jackett/api/v2.0/indexers/all/results?Query=Media%20Stack%20Test&Category[]=2000")
    results = json.loads(body).get("Results", []) if status == 200 else []
    seeded = [r for r in results if r.get("Tracker") == "Local test seeder"]
    check("search returns the seeder release", bool(seeded))
    if not seeded:
        return
    check("release link is an opaque gateway link", seeded[0]["MagnetUri"].startswith(BASE + "/t/"))
    status, body = http("POST", BASE + "/ts/torrents",
                        json.dumps({"action": "add", "link": seeded[0]["MagnetUri"], "title": "[LAMPA] leak test"}))
    torrent = json.loads(body) if status == 200 else {}
    check("add through the gateway", bool(torrent.get("hash")))
    if torrent.get("hash"):
        status, chunk = http("GET", f"{BASE}/ts/stream/clip.mp4?link={torrent['hash']}&index=1&play",
                             headers={"Range": "bytes=0-1048575"}, limit=1 << 20)
        check("stream returns bytes", status in (200, 206) and len(chunk) > 0)
    http("POST", BASE + "/ts/torrents", '{"action":"list"}')

    # Leak scan.
    responses = b"\n".join(client_bodies).decode("utf8", "replace")
    for name, value in CLIENT_SECRETS.items():
        check(f"{name} absent from client responses", value not in responses)
    logs = compose("--profile", "test", "logs", "--no-log-prefix")
    for name, value in STORED_SECRETS.items():
        check(f"{name} absent from container logs", value not in logs)
    stored = compose("exec", "-T", "torrserver", "sh", "-c",
                     "find /opt/ts/config -type f ! -name accs.db -exec cat {} +")
    for name, value in STORED_SECRETS.items():
        check(f"{name} absent from TorrServer's stored data", value not in stored)


if __name__ == "__main__":
    main()
    print(f"\n{len(failures)} failed" if failures else "\nall checks passed")
    sys.exit(1 if failures else 0)
