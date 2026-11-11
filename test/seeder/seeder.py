"""Test seeder for local and CI runs.

Serves on :9117
  /announce          a minimal HTTP tracker that always returns this container as the only peer
  /api?t=caps|search a one-item Torznab feed, added to Prowlarr as a Generic Torznab indexer
  /test.torrent      the torrent for the generated clip
and seeds the clip with aria2c on :6881. Test-only code: never part of a production deployment.
"""
import os
import socket
import struct
import subprocess
from email.utils import formatdate
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

MEDIA = "/media"
TORRENT = os.path.join(MEDIA, "test.torrent")
CLIP = "Media Stack Test Clip (2026).mp4"
TITLE = "Media Stack Test Clip (2026) 720p WEB-DL H.264 AAC"
PEER_PORT = 6881
HOST = os.environ.get("SEEDER_HOST", "seeder")

CAPS = b"""<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server title="media-stack test seeder"/>
  <limits max="100" default="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q"/>
    <movie-search available="yes" supportedParams="q"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"/>
    <category id="5000" name="TV"/>
  </categories>
</caps>
"""


def feed():
    size = os.path.getsize(os.path.join(MEDIA, CLIP))
    url = f"http://{HOST}:9117/test.torrent"
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel><title>media-stack test seeder</title>
<item>
  <title>{TITLE}</title>
  <guid>media-stack-test-clip</guid>
  <link>{url}</link>
  <pubDate>{formatdate(usegmt=True)}</pubDate>
  <size>{size}</size>
  <enclosure url="{url}" length="{size}" type="application/x-bittorrent"/>
  <torznab:attr name="category" value="2000"/>
  <torznab:attr name="size" value="{size}"/>
  <torznab:attr name="seeders" value="5"/>
  <torznab:attr name="peers" value="5"/>
</item>
</channel></rss>
""".encode()


def own_ip():
    return socket.gethostbyname(socket.gethostname())


class Handler(BaseHTTPRequestHandler):
    def send(self, body, ctype):
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/announce":
            peer = socket.inet_aton(own_ip()) + struct.pack(">H", PEER_PORT)
            self.send(b"d8:intervali60e5:peers6:" + peer + b"e", "text/plain")
        elif u.path == "/api":
            t = parse_qs(u.query).get("t", ["search"])[0]
            if t == "caps":
                self.send(CAPS, "application/xml")
            else:  # every search returns the one test release
                self.send(feed(), "application/rss+xml")
        elif u.path == "/test.torrent":
            with open(TORRENT, "rb") as f:
                self.send(f.read(), "application/x-bittorrent")
        else:
            self.send_error(404)

    def log_message(self, fmt, *args):
        print("http", self.command, urlparse(self.path).path, args[1] if len(args) > 1 else "")


if __name__ == "__main__":
    subprocess.Popen([
        "aria2c", "--dir", MEDIA, "--listen-port", str(PEER_PORT),
        "--enable-dht=false", "--bt-enable-lpd=false", "--enable-peer-exchange=false",
        "--seed-ratio=0.0", "--bt-seed-unverified=true", "--summary-interval=0", "--show-console-readout=false",
        "--console-log-level=warn", TORRENT,
    ])
    print("seeding", CLIP, "on", own_ip())
    ThreadingHTTPServer(("0.0.0.0", 9117), Handler).serve_forever()
