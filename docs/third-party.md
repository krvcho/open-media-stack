# Third-party software and data

This repository contains only its own code (MIT, [LICENSE](../LICENSE)). Everything below is pulled from its
own registry or repository at build or install time, pinned by version and digest, and keeps its own
licence. Licences as GitHub reports them for each repository, checked 2026-11-11.

## Runtime: media-stack

| Component | Image | Source | Licence |
|---|---|---|---|
| Lampa (web build, downloaded by the gateway at a pinned commit) | — | build [yumata/lampa](https://github.com/yumata/lampa), source [yumata/lampa-source](https://github.com/yumata/lampa-source) | GPL-2.0 |
| TorrServer | `ghcr.io/yourok/torrserver` | [YouROK/TorrServer](https://github.com/YouROK/TorrServer) | GPL-3.0 |
| Prowlarr | `ghcr.io/linuxserver/prowlarr` | [Prowlarr/Prowlarr](https://github.com/Prowlarr/Prowlarr), image [linuxserver/docker-prowlarr](https://github.com/linuxserver/docker-prowlarr) | GPL-3.0 |
| Lampac | `ghcr.io/lampac-nextgen/lampac` | [lampac-nextgen/lampac](https://github.com/lampac-nextgen/lampac) | AGPL-3.0 |
| FlareSolverr (optional) | `ghcr.io/flaresolverr/flaresolverr` | [FlareSolverr/FlareSolverr](https://github.com/FlareSolverr/FlareSolverr) | MIT |
| gluetun (optional VPN) | `qmcgaw/gluetun` | [qdm12/gluetun](https://github.com/qdm12/gluetun) | MIT |
| Gateway base image | `gcr.io/distroless/static-debian12` | [GoogleContainerTools/distroless](https://github.com/GoogleContainerTools/distroless) | Apache-2.0 |

### The Lampa web build served by the gateway

At first start the gateway downloads Lampa's published web build from
[yumata/lampa](https://github.com/yumata/lampa/tree/3171201b10a38816378212756b963428836dbf13) at the commit pinned in `compose.yaml`
(`GW_LAMPA_COMMIT`), checks its SHA-256 and serves it to your devices. It changes one file: `index.html` gets a
`<script>` that sets Lampa's settings (`injectSettings` in `gateway/lampa.go`); everything else is served as
published. Lampa is GPL-2.0; its author publishes the build at the link above and its source code in
[yumata/lampa-source](https://github.com/yumata/lampa-source). The change and the plugins the gateway adds to
Lampa are this repository's code under the MIT licence, which is
compatible with the GPL-2.0, so the served whole may be used and redistributed under the GPL-2.0.

Online sources in Lampac are Lampac's modules; this repository configures and names none of them.
Lampac is AGPL-3.0: it runs unmodified from its upstream image, and its source is at the link above.

## Runtime: monitoring stack (optional)

| Component | Image | Source | Licence |
|---|---|---|---|
| Prometheus | `prom/prometheus` | [prometheus/prometheus](https://github.com/prometheus/prometheus) | Apache-2.0 |
| Alertmanager | `prom/alertmanager` | [prometheus/alertmanager](https://github.com/prometheus/alertmanager) | Apache-2.0 |
| Loki | `grafana/loki` | [grafana/loki](https://github.com/grafana/loki) | AGPL-3.0 |
| Grafana | `grafana/grafana` | [grafana/grafana](https://github.com/grafana/grafana) | AGPL-3.0 |
| Grafana Alloy | `grafana/alloy` | [grafana/alloy](https://github.com/grafana/alloy) | Apache-2.0 |

The monitoring images carry this repository's configuration (copied in at build time); their code is unmodified.

## Build, test and scripts

| Component | Used for | Source | Licence |
|---|---|---|---|
| Go toolchain image `golang` | building the gateway | [docker-library/golang](https://github.com/docker-library/golang) | BSD-3-Clause |
| Alpine Linux image `alpine` | the test seeder, `scripts/backup.sh` and `restore.sh` | [alpinelinux/docker-alpine](https://github.com/alpinelinux/docker-alpine) | MIT |
| windows_exporter (documented, not shipped) | a Windows PC in monitoring (install step 9) | [prometheus-community/windows_exporter](https://github.com/prometheus-community/windows_exporter) | MIT |

## Go modules of the gateway

All permissive.

| Module | Licence |
|---|---|
| `github.com/prometheus/client_golang`, `client_model`, `common`, `procfs` | Apache-2.0 |
| `go.yaml.in/yaml/v3` | MIT and Apache-2.0 |
| `github.com/beorn7/perks` | MIT |
| `github.com/cespare/xxhash/v2` | MIT |
| `github.com/munnerz/goautoneg` | BSD-3-Clause (licence text in the module; GitHub reports no SPDX id) |
| `golang.org/x/sys` | BSD-3-Clause |
| `google.golang.org/protobuf` | BSD-3-Clause |

## Data services

* **TMDB:** catalogue, artwork and streaming availability. This product uses the TMDB API but is not endorsed or
  certified by TMDB. Each install uses its own TMDB API key under TMDB's terms (install step 4).
* **JustWatch:** streaming availability in the Platforms screen comes from JustWatch through TMDB.
* **Search sources (indexers):** none are shipped or configured. Each user adds the ones they have access to in Prowlarr
  and is responsible for using them lawfully.
