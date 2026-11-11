"""Builds the provisioned Grafana dashboards in dashboards/.

The JSON files are generated: change this script, run `python3 build_dashboards.py`, commit both. CI runs
`python3 build_dashboards.py --check` and fails when the JSON and the script disagree.
"""
import json
import pathlib
import sys

P = {"type": "prometheus", "uid": "prometheus"}
L = {"type": "loki", "uid": "loki"}
MIXED = {"type": "datasource", "uid": "-- Mixed --"}
OUT = pathlib.Path(__file__).with_name("dashboards")

GREEN = [{"color": "green", "value": None}]
UPDOWN = [{"color": "red", "value": None}, {"color": "green", "value": 1}]
ZERO_OK = [{"color": "green", "value": None}, {"color": "red", "value": 1}]


def warn(a, b):
    return [{"color": "green", "value": None}, {"color": "orange", "value": a}, {"color": "red", "value": b}]


# cAdvisor labels: a Compose project becomes `project` and a container without one shows as "standalone".
def by_project(expr):
    e = f'label_replace({expr}, "project", "$1", "container_label_com_docker_compose_project", "(.+)")'
    return f'label_replace({e}, "project", "standalone", "project", "")'


class Dash:
    def __init__(self, uid, title, tags, time_from="now-6h"):
        self.uid, self.title, self.tags, self.time_from = uid, title, tags, time_from
        self.panels, self.y, self.vars, self.links = [], 0, [], []

    def row(self, title, repeat=None):
        p = {"type": "row", "title": title, "collapsed": False, "panels": [],
             "gridPos": {"x": 0, "y": self.y, "w": 24, "h": 1}, "id": len(self.panels) + 1}
        if repeat:
            p["repeat"] = repeat
        self.panels.append(p)
        self.y += 1

    def place(self, h, *items):
        x = 0
        for p, w in items:
            p["id"] = len(self.panels) + 1
            p["gridPos"] = {"x": x, "y": self.y, "w": w, "h": h}
            self.panels.append(p)
            x += w
        self.y += h

    def var(self, name, label, query, multi=True, include_all=True, hide=0):
        self.vars.append({"name": name, "label": label, "type": "query", "datasource": P, "hide": hide,
                          "query": {"query": query, "refId": name}, "definition": query, "refresh": 2, "sort": 1,
                          "multi": multi, "includeAll": include_all, "allValue": ".*" if include_all else None,
                          "current": {"text": "All", "value": "$__all"} if include_all else {}})

    def save(self):
        d = {"uid": self.uid, "title": self.title, "tags": self.tags, "timezone": "browser", "editable": False,
             "graphTooltip": 1, "schemaVersion": 41, "version": 1, "refresh": "30s",
             "time": {"from": self.time_from, "to": "now"}, "links": self.links,
             "templating": {"list": self.vars}, "panels": self.panels}
        return self.uid + ".json", json.dumps(d, indent=2, ensure_ascii=False) + "\n"


def nav():
    return [{"type": "dashboards", "tags": ["monitoring"], "asDropdown": True, "title": "Monitoring",
             "includeVars": False, "keepTime": True}]


def stat(title, expr, unit="none", steps=GREEN, decimals=None, graph=True, ds=P, color_mode="value", text=None,
         mappings=None, instant=False):
    t = {"refId": "A", "expr": expr, "datasource": ds}
    if instant:
        t["instant"] = True
    p = {"type": "stat", "title": title, "datasource": ds, "targets": [t],
         "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": color_mode,
                     "graphMode": "area" if graph else "none", "textMode": "value", "justifyMode": "center"},
         "fieldConfig": {"defaults": {"unit": unit, "thresholds": {"mode": "absolute", "steps": steps},
                                      "mappings": mappings or [], "color": {"mode": "thresholds"}},
                         "overrides": []}}
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if text:
        p["options"]["text"] = text
    return p


def gauge(title, expr, unit="percentunit", steps=None, mx=1, no_value=None):
    return {"type": "gauge", "title": title, "datasource": P, "targets": [{"refId": "A", "expr": expr, "datasource": P}],
            "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "showThresholdMarkers": True},
            "fieldConfig": {"defaults": {"unit": unit, "min": 0, "max": mx, "decimals": 0, "noValue": no_value,
                                         "thresholds": {"mode": "absolute", "steps": steps or warn(0.75, 0.9)}},
                            "overrides": []}}


def ts(title, targets, unit, stack=False, fill=12, legend="table", ds=P, bars=False, overrides=None, mn=None):
    custom = {"fillOpacity": 60 if bars else fill, "gradientMode": "opacity", "lineWidth": 1, "showPoints": "never",
              "stacking": {"mode": "normal" if stack else "none"}, "drawStyle": "bars" if bars else "line"}
    p = {"type": "timeseries", "title": title, "datasource": ds,
         "targets": [{"refId": chr(65 + i), "expr": e, "legendFormat": lf, "datasource": ds}
                     for i, (e, lf) in enumerate(targets)],
         "options": {"legend": {"displayMode": legend, "placement": "bottom" if legend == "list" else "right",
                                "calcs": [] if legend == "list" else ["mean", "max"]},
                     "tooltip": {"mode": "multi", "sort": "desc"}},
         "fieldConfig": {"defaults": {"unit": unit, "custom": custom}, "overrides": overrides or []}}
    if mn is not None:
        p["fieldConfig"]["defaults"]["min"] = mn
    return p


def bargauge(title, expr, legend, unit, steps=None, mx=None):
    # Absolute amounts in one hue; a ratio with thresholds (steps) colours by them.
    d = {"unit": unit, "min": 0, "thresholds": {"mode": "absolute", "steps": steps or GREEN},
         "color": {"mode": "thresholds" if steps else "continuous-BlPu"}}
    if mx is not None:
        d["max"] = mx
    return {"type": "bargauge", "title": title, "datasource": P,
            "targets": [{"refId": "A", "expr": expr, "legendFormat": legend, "instant": True, "datasource": P}],
            "options": {"orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
                        "valueMode": "text", "namePlacement": "left", "sizing": "manual", "minVizHeight": 18,
                        "maxVizHeight": 22, "minVizWidth": 8,
                        "reduceOptions": {"calcs": ["lastNotNull"]}},
            "fieldConfig": {"defaults": d, "overrides": []}}


def logs(title, expr):
    return {"type": "logs", "title": title, "datasource": L, "targets": [{"refId": "A", "datasource": L, "expr": expr}],
            "options": {"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending",
                        "enableLogDetails": True, "prettifyLogMessage": False, "dedupStrategy": "none"}}


LEVEL_COLORS = [{"matcher": {"id": "byName", "options": n}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": c}}]}
                for n, c in [("error", "red"), ("warn", "orange"), ("warning", "orange"), ("info", "green"), ("debug", "blue")]]
IN_OUT = [{"matcher": {"id": "byRegexp", "options": ".* out$|^out$|sent|write"},
           "properties": [{"id": "custom.transform", "value": "negative-Y"}]}]


# ---------------------------------------------------------------- Host and containers (the Linux server)

def host_containers():
    d = Dash("host-containers", "Host and containers", ["monitoring"])
    d.links = nav()
    d.var("project", "Compose project",
          "label_values(container_memory_working_set_bytes, container_label_com_docker_compose_project)")
    C = 'name!="", container_label_com_docker_compose_project=~"$project"'
    NIC = 'device!~"lo|veth.*|br-.*|docker.*|tailscale.*"'
    d.row("Host")
    d.place(4,
            (stat("CPU busy", '1 - avg(rate(node_cpu_seconds_total{mode="idle"}[$__rate_interval]))', "percentunit", warn(0.7, 0.9), 0), 4),
            (stat("Memory used", "1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes", "percentunit", warn(0.8, 0.9), 0), 4),
            (stat("Root disk used", '1 - node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"}', "percentunit", warn(0.8, 0.9), 0), 4),
            (stat("Hottest sensor", "max(node_hwmon_temp_celsius)", "celsius", warn(70, 80), 0), 4),
            (stat("Load (1 m)", "node_load1", "short", GREEN, 2), 4),
            (stat("Uptime", "time() - node_boot_time_seconds", "s", GREEN, 0, graph=False), 4))
    d.place(8,
            (ts("CPU by mode", [('sum by (mode) (rate(node_cpu_seconds_total{mode!="idle"}[$__rate_interval])) / scalar(count(node_cpu_seconds_total{mode="idle"}))', "{{mode}}")], "percentunit", stack=True), 12),
            (ts("Memory", [("node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes", "used"), ("node_memory_Cached_bytes + node_memory_Buffers_bytes", "cache"), ("node_memory_MemAvailable_bytes", "available")], "bytes"), 12))
    d.place(8,
            (ts("Network", [(f'sum by (device) (rate(node_network_receive_bytes_total{{{NIC}}}[$__rate_interval]))', "{{device}} in"), (f'sum by (device) (rate(node_network_transmit_bytes_total{{{NIC}}}[$__rate_interval]))', "{{device}} out")], "Bps", overrides=IN_OUT), 8),
            (ts("Disk throughput", [('sum by (device) (rate(node_disk_read_bytes_total{device!~"loop.*|ram.*"}[$__rate_interval]))', "{{device}} read"), ('sum by (device) (rate(node_disk_written_bytes_total{device!~"loop.*|ram.*"}[$__rate_interval]))', "{{device}} write")], "Bps", overrides=IN_OUT), 8),
            (ts("Temperature", [("node_hwmon_temp_celsius", "{{chip}} {{sensor}}")], "celsius"), 8))
    d.row("Containers")
    d.place(9,
            (ts("CPU per container", [(f"sum by (name) (rate(container_cpu_usage_seconds_total{{{C}}}[$__rate_interval]))", "{{name}}")], "percentunit"), 12),
            (ts("Memory per container (working set)", [(f"sum by (name) (container_memory_working_set_bytes{{{C}}})", "{{name}}")], "bytes"), 12))
    d.place(9,
            (ts("Network per container", [(f"sum by (name) (rate(container_network_receive_bytes_total{{{C}}}[$__rate_interval]))", "{{name}} in"), (f"sum by (name) (rate(container_network_transmit_bytes_total{{{C}}}[$__rate_interval]))", "{{name}} out")], "Bps", overrides=IN_OUT), 12),
            (ts("Disk writes per container", [(f"sum by (name) (rate(container_fs_writes_bytes_total{{{C}}}[$__rate_interval]))", "{{name}}")], "Bps"), 12))
    d.row("Alerts and logs")
    d.place(8,
            ({"type": "alertlist", "title": "Firing and pending alerts",
              "options": {"viewMode": "list", "showInstances": True, "stateFilter": {"firing": True, "pending": True},
                          "groupMode": "default", "maxItems": 20, "sortOrder": 1, "datasource": "Prometheus"}}, 8),
            (ts("Log lines by level", [('sum by (level) (count_over_time({project=~"$project", level=~".+"} [$__auto]))', "{{level}}")], "short", stack=True, bars=True, legend="list", ds=L, overrides=LEVEL_COLORS), 16))
    d.place(10, (logs("Warnings and errors", '{project=~"$project", level=~"warn|warning|error|err|fatal|crit|critical|panic"}'), 24))
    return d


# ---------------------------------------------------------------- Stacks: every Compose project at a glance

def stacks():
    d = Dash("stacks", "Stacks", ["monitoring"])
    d.links = nav()
    W = 'container_memory_working_set_bytes{name!=""}'
    d.row("All stacks")
    d.place(4,
            (stat("Stacks", f'count(count by (project) ({by_project(W)}))', graph=False), 4),
            (stat("Containers", f"count({W})", graph=False), 4),
            (stat("Containers CPU", 'sum(rate(container_cpu_usage_seconds_total{name!=""}[$__rate_interval]))', "percentunit", warn(2, 3), 1), 4),
            (stat("Containers memory", f"sum({W})", "bytes", GREEN, 1), 4),
            (stat("Errors in logs (1 h)", 'sum(count_over_time({level=~"error|err|fatal|crit|critical|panic"} [1h])) or vector(0)', "short", warn(1, 50), 0, ds=L), 4),
            (stat("Firing alerts", 'count(ALERTS{alertstate="firing"}) or vector(0)', "short", ZERO_OK, 0), 4))

    # One row per stack: instant values merged on `project`. The project cell opens the Container dashboard.
    q = [
        (f'count by (project) ({by_project(W)})', "Containers"),
        (f'sum by (project) ({by_project("rate(container_cpu_usage_seconds_total{name!=\"\"}[5m])")})', "CPU"),
        (f'sum by (project) ({by_project(W)})', "Memory"),
        (f'sum by (project) ({by_project("rate(container_network_receive_bytes_total{name!=\"\"}[5m])")})', "Net in"),
        (f'sum by (project) ({by_project("rate(container_network_transmit_bytes_total{name!=\"\"}[5m])")})', "Net out"),
        (f'sum by (project) ({by_project("rate(container_fs_writes_bytes_total{name!=\"\"}[5m])")})', "Disk writes"),
    ]
    targets = [{"refId": chr(65 + i), "expr": e, "instant": True, "format": "table", "datasource": P} for i, (e, _) in enumerate(q)]
    targets.append({"refId": "Z", "datasource": L, "queryType": "instant", "expr":
                    'sum by (project) (count_over_time({project=~".+", level=~"error|err|fatal|crit|critical|panic"} [1h]))'})
    rename = {f"Value #{chr(65 + i)}": name for i, (_, name) in enumerate(q)}
    rename["Value #Z"] = "Errors (1 h)"

    def col(name, unit, cell=None, decimals=None, extra=()):
        props = [{"id": "unit", "value": unit}, *extra]
        if decimals is not None:
            props.append({"id": "decimals", "value": decimals})
        if cell:
            props.append({"id": "custom.cellOptions", "value": cell})
        return {"matcher": {"id": "byName", "options": name}, "properties": props}

    bar = {"type": "gauge", "mode": "lcd", "valueDisplayMode": "text"}
    table = {"type": "table", "title": "Stacks (click a name for its containers)", "datasource": MIXED, "targets": targets,
             "transformations": [{"id": "merge", "options": {}},
                                 {"id": "organize", "options": {"excludeByName": {"Time": True, "Time 1": True},
                                                                "renameByName": dict(rename, project="Stack")}},
                                 {"id": "sortBy", "options": {"sort": [{"field": "Memory", "desc": True}]}}],
             "options": {"showHeader": True, "cellHeight": "md", "footer": {"show": False}},
             "fieldConfig": {"defaults": {"custom": {"align": "auto", "filterable": False},
                                          "thresholds": {"mode": "absolute", "steps": GREEN}},
                             "overrides": [
                                 {"matcher": {"id": "byName", "options": "Stack"}, "properties": [
                                     {"id": "links", "value": [{"title": "Containers of ${__value.text}",
                                                                "url": "/d/container/container?var-project=${__value.raw}&${__url_time_range}"}]},
                                     {"id": "custom.width", "value": 180}]},
                                 col("Containers", "none", decimals=0),
                                 col("CPU", "percentunit", bar, 1, [{"id": "max", "value": 1}, {"id": "color", "value": {"mode": "continuous-GrYlRd"}}]),
                                 col("Memory", "bytes", bar, 1, [{"id": "color", "value": {"mode": "continuous-BlPu"}}]),
                                 col("Net in", "Bps", decimals=1), col("Net out", "Bps", decimals=1), col("Disk writes", "Bps", decimals=1),
                                 {"matcher": {"id": "byName", "options": "Errors (1 h)"}, "properties": [
                                     {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}},
                                     {"id": "thresholds", "value": {"mode": "absolute", "steps": warn(1, 50)}},
                                     {"id": "noValue", "value": "0"}]},
                             ]}}
    d.place(9, (table, 24))
    d.place(9,
            (ts("CPU by stack", [(f'sum by (project) ({by_project("rate(container_cpu_usage_seconds_total{name!=\"\"}[$__rate_interval])")})', "{{project}}")], "percentunit", stack=True, fill=40), 12),
            (ts("Memory by stack", [(f'sum by (project) ({by_project(W)})', "{{project}}")], "bytes", stack=True, fill=40), 12))
    d.place(9,
            (ts("Network by stack", [(f'sum by (project) ({by_project("rate(container_network_receive_bytes_total{name!=\"\"}[$__rate_interval])")})', "{{project}} in"),
                                     (f'sum by (project) ({by_project("rate(container_network_transmit_bytes_total{name!=\"\"}[$__rate_interval])")})', "{{project}} out")], "Bps", overrides=IN_OUT), 12),
            (ts("Log lines by stack", [('sum by (project) (count_over_time({project=~".+"} [$__auto]))', "{{project}}")], "short", stack=True, bars=True, legend="list", ds=L), 12))
    d.row("Top containers")
    d.place(10,
            (bargauge("Memory", f"topk(10, {W})", "{{name}}", "bytes"), 8),
            (bargauge("CPU", 'topk(10, rate(container_cpu_usage_seconds_total{name!=""}[5m]))', "{{name}}", "percentunit"), 8),
            (bargauge("Memory of its limit", 'topk(10, container_memory_working_set_bytes{name!=""} / (container_spec_memory_limit_bytes{name!=""} > 0))', "{{name}}", "percentunit", warn(0.75, 0.9), 1), 8))
    return d


# ---------------------------------------------------------------- Container: one repeated row per container

def container():
    d = Dash("container", "Container", ["monitoring"], "now-3h")
    d.links = nav()
    d.var("project", "Stack", 'label_values(container_memory_working_set_bytes{name!=""}, container_label_com_docker_compose_project)')
    d.var("container", "Container", 'label_values(container_memory_working_set_bytes{name!="", container_label_com_docker_compose_project=~"$project"}, name)')
    N = 'name="$container"'
    d.row("$container", repeat="container")
    d.place(4,
            (stat("Up for", f"time() - max(container_start_time_seconds{{{N}}})", "s", GREEN, 0, graph=False), 4),
            (stat("Recreated (24 h)", f"count(count by (id) (count_over_time(container_start_time_seconds{{{N}}}[24h]))) - 1", "none", warn(1, 3), 0, graph=False), 4),
            (stat("CPU", f"sum(rate(container_cpu_usage_seconds_total{{{N}}}[$__rate_interval]))", "percentunit", warn(0.5, 0.9), 1), 4),
            (stat("Memory", f"sum(container_memory_working_set_bytes{{{N}}})", "bytes", GREEN, 1), 4),
            (gauge("Memory of its limit", f"sum(container_memory_working_set_bytes{{{N}}}) / sum(container_spec_memory_limit_bytes{{{N}}} > 0)", no_value="no limit"), 4),
            (stat("Errors in logs (1 h)", 'sum(count_over_time({container="$container", level=~"error|err|fatal|crit|critical|panic"} [1h])) or vector(0)', "short", warn(1, 20), 0, ds=L), 4))
    d.place(8,
            (ts("CPU", [(f"sum(rate(container_cpu_usage_seconds_total{{{N}}}[$__rate_interval]))", "used"),
                        (f"sum(rate(container_cpu_cfs_throttled_seconds_total{{{N}}}[$__rate_interval]))", "throttled")], "percentunit", legend="list", mn=0), 6),
            (ts("Memory", [(f"sum(container_memory_working_set_bytes{{{N}}})", "working set"),
                           (f"sum(container_memory_rss{{{N}}})", "rss"),
                           (f"sum(container_memory_cache{{{N}}})", "page cache"),
                           (f"sum(container_spec_memory_limit_bytes{{{N}}} > 0)", "limit")], "bytes", legend="list",
                overrides=[{"matcher": {"id": "byName", "options": "limit"}, "properties": [
                    {"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 10]}}, {"id": "custom.fillOpacity", "value": 0},
                    {"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]}]), 6),
            (ts("Network", [(f"sum(rate(container_network_receive_bytes_total{{{N}}}[$__rate_interval]))", "in"),
                            (f"sum(rate(container_network_transmit_bytes_total{{{N}}}[$__rate_interval]))", "out")], "Bps", legend="list", overrides=IN_OUT), 6),
            (ts("Disk", [(f"sum(rate(container_fs_reads_bytes_total{{{N}}}[$__rate_interval]))", "read"),
                         (f"sum(rate(container_fs_writes_bytes_total{{{N}}}[$__rate_interval]))", "write")], "Bps", legend="list", overrides=IN_OUT), 6))
    d.place(9,
            (ts("Log lines by level", [('sum by (level) (count_over_time({container="$container"} [$__auto]))', "{{level}}")], "short", stack=True, bars=True, legend="list", ds=L, overrides=LEVEL_COLORS), 8),
            (logs("Logs", '{container="$container"}'), 16))
    return d


# ---------------------------------------------------------------- Windows PC (windows_exporter)

def windows():
    d = Dash("windows", "Windows PC", ["monitoring"], "now-6h")
    d.links = nav()
    d.var("instance", "Computer", "label_values(windows_os_info, instance)", multi=False, include_all=False)
    I = 'instance="$instance"'
    CPU = f'1 - avg(rate(windows_cpu_time_seconds_total{{{I}, mode="idle"}}[$__rate_interval]))'
    MEM = f"1 - windows_memory_available_bytes{{{I}}} / windows_memory_physical_total_bytes{{{I}}}"
    # windows_exporter 0.32 labels: engtype (not engine_type as in its docs), one series per process; an engine's
    # load is the sum over processes, a type's load the busiest engine of that type (as Task Manager shows it).
    ENG = f"sum by (luid, phys, eng, engtype) (rate(windows_gpu_engine_time_seconds_total{{{I}}}[$__rate_interval]))"
    GPU3D = f'max(sum by (luid, phys, eng) (rate(windows_gpu_engine_time_seconds_total{{{I}, engtype="3D"}}[$__rate_interval])))'
    NAMED = f" * on (luid) group_left (name) windows_gpu_info{{{I}}}" # luid -> the card's name
    d.row("Now")
    d.place(5,
            (gauge("CPU", CPU), 4),
            (gauge("Memory", MEM), 4),
            (gauge("GPU (3D)", GPU3D), 4),
            (stat("GPU temperature", f"max(windows_gpu_temperature_celsius{{{I}}})", "celsius", warn(75, 85), 0), 4),
            (stat("System drive free", f'windows_logical_disk_free_bytes{{{I}, volume="C:"}} / windows_logical_disk_size_bytes{{{I}, volume="C:"}}', "percentunit",
                  [{"color": "red", "value": None}, {"color": "orange", "value": 0.1}, {"color": "green", "value": 0.2}], 0, graph=False), 4),
            (stat("Up for", f"time() - windows_system_boot_time_timestamp{{{I}}}", "s", GREEN, 0, graph=False), 4))
    d.row("CPU and memory")
    d.place(8,
            (ts("CPU by mode", [(f'sum by (mode) (rate(windows_cpu_time_seconds_total{{{I}, mode!="idle"}}[$__rate_interval])) / scalar(count(windows_cpu_time_seconds_total{{{I}, mode="idle"}}))', "{{mode}}")], "percentunit", stack=True, fill=40, legend="list"), 8),
            (ts("CPU per core", [(f'1 - rate(windows_cpu_time_seconds_total{{{I}, mode="idle"}}[$__rate_interval])', "{{core}}")], "percentunit", legend="hidden", mn=0), 8),
            (ts("Memory", [(f"windows_memory_physical_total_bytes{{{I}}} - windows_memory_available_bytes{{{I}}}", "in use"),
                           (f"windows_memory_cache_bytes{{{I}}}", "cache"),
                           (f"windows_memory_committed_bytes{{{I}}}", "committed")], "bytes", legend="list"), 8))
    d.row("GPU")
    d.place(8,
            (ts("GPU engines (busy ones)", [(f"max by (engtype) ({ENG}) > 0", "{{engtype}}")], "percentunit", legend="list", mn=0), 8),
            (ts("Video memory per card", [(f"sum by (luid) (windows_gpu_adapter_memory_dedicated_bytes{{{I}}}){NAMED}", "{{name}} in use"),
                                          (f"sum by (luid) (windows_gpu_dedicated_video_memory_size_bytes{{{I}}}){NAMED}", "{{name}} size")], "bytes", legend="list"), 8),
            (ts("GPU temperature and fan", [(f"max(windows_gpu_temperature_celsius{{{I}}})", "°C"), (f"max(windows_gpu_fan_speed_rpm{{{I}}})", "fan rpm")], "short", legend="list"), 8))
    d.row("Disks and network")
    d.place(8,
            (bargauge("Space used per drive", f'1 - windows_logical_disk_free_bytes{{{I}, volume=~"[A-Z]:"}} / windows_logical_disk_size_bytes{{{I}, volume=~"[A-Z]:"}}', "{{volume}}", "percentunit", warn(0.8, 0.9), 1), 8),
            (ts("Disk throughput", [(f'sum by (volume) (rate(windows_logical_disk_read_bytes_total{{{I}, volume=~"[A-Z]:"}}[$__rate_interval]))', "{{volume}} read"),
                                    (f'sum by (volume) (rate(windows_logical_disk_write_bytes_total{{{I}, volume=~"[A-Z]:"}}[$__rate_interval]))', "{{volume}} write")], "Bps", legend="list", overrides=IN_OUT), 8),
            (ts("Network", [(f'sum by (nic) (rate(windows_net_bytes_received_total{{{I}}}[$__rate_interval])) > 0', "{{nic}} in"),
                            (f'sum by (nic) (rate(windows_net_bytes_sent_total{{{I}}}[$__rate_interval])) > 0', "{{nic}} out")], "Bps", legend="list", overrides=IN_OUT), 8))
    return d


# ---------------------------------------------------------------- media-stack

def media_stack():
    d = Dash("media-stack", "media-stack", ["monitoring", "media-stack"], "now-24h")
    d.links = nav()
    G = 'job="gateway"'
    d.row("Overview")
    d.place(4,
            (stat("Gateway", f"max(up{{{G}}})", "none", UPDOWN, 0, graph=False, color_mode="background", mappings=UPMAP), 3),
            *[(stat(name, f'max(gateway_upstream_up{{service="{svc}"}})', "none", UPDOWN, 0, graph=False, color_mode="background", mappings=UPMAP), 3)
              for name, svc in [("Prowlarr", "prowlarr"), ("TorrServer", "torrserver"), ("Lampac", "lampac"), ("VPN", "vpn")]],
            (stat("Uploaded to peers (24 h)", "sum(increase(gateway_torrent_upload_bytes_total[24h]))", "bytes", ZERO_OK, 0, graph=False, color_mode="background"), 3),
            (stat("Settings drift", "max(gateway_torrserver_settings_drift)", "none", ZERO_OK, 0, graph=False, color_mode="background"), 3),
            (stat("Rejected tokens (24 h)", "sum(increase(gateway_auth_rejected_total[24h]))", "none", warn(1, 20), 0, graph=False), 3))
    d.place(4,
            (stat("Search p95 (SLO 5 s)", "histogram_quantile(0.95, sum by (le) (rate(gateway_search_duration_seconds_bucket[$__range])))", "s", warn(4, 5), 2), 5),
            (stat("Stream TTFB p95 (SLO 5 s)", "histogram_quantile(0.95, sum by (le) (rate(gateway_stream_ttfb_seconds_bucket[$__range])))", "s", warn(4, 5), 2), 5),
            (stat("First frame p95 (SLO 15 s)", "histogram_quantile(0.95, sum by (le) (rate(client_ttff_seconds_bucket[$__range])))", "s", warn(10, 15), 1), 5),
            (stat("Gateway availability", f"avg_over_time(up{{{G}}}[$__range])", "percentunit", [{"color": "red", "value": None}, {"color": "orange", "value": 0.98}, {"color": "green", "value": 0.99}], 2), 5),
            (stat("Search errors", "(sum(increase(gateway_search_errors_total[$__range])) or vector(0)) / sum(increase(gateway_search_duration_seconds_count[$__range]))", "percentunit", warn(0.1, 0.2), 1), 4))
    d.place(8,
            (ts("Requests by route", [("sum by (route) (rate(gateway_http_requests_total[$__rate_interval]))", "{{route}}")], "reqps", stack=True, fill=40), 12),
            (ts("Upstream probe duration", [("max by (service) (gateway_upstream_probe_duration_seconds)", "{{service}}")], "s"), 12))
    d.row("Streaming")
    d.place(8,
            (ts("Stream throughput", [("rate(gateway_stream_bytes_total[$__rate_interval])", "to clients"), ("sum(gateway_torrent_download_bytes_per_second)", "from swarm")], "Bps", legend="list"), 12),
            (ts("Streams, torrents and peers", [("sum(gateway_streams_active)", "streams"), ("sum(gateway_torrents_active)", "torrents"), ("sum by (state) (gateway_torrent_peers)", "peers {{state}}")], "short", legend="list"), 12))
    d.place(8,
            (ts("Stream time to first byte", [("histogram_quantile(0.5, sum by (le) (rate(gateway_stream_ttfb_seconds_bucket[$__rate_interval])))", "p50"), ("histogram_quantile(0.95, sum by (le) (rate(gateway_stream_ttfb_seconds_bucket[$__rate_interval])))", "p95")], "s", legend="list"), 8),
            (ts("TorrServer cache", [("sum(gateway_torrent_preloaded_bytes)", "preloaded")], "bytes", legend="list"), 8),
            (ts("Uploaded piece data (must stay 0)", [("sum(increase(gateway_torrent_upload_bytes_total[$__rate_interval]))", "uploaded")], "bytes", legend="list"), 8))
    # The playback beacon: Lampa's own player only (browser, Tizen, webOS); external players report nothing.
    d.row("Playback in Lampa's own player")
    d.place(8,
            (ts("Time to first frame", [("histogram_quantile(0.5, sum by (le) (rate(client_ttff_seconds_bucket[$__rate_interval])))", "p50"), ("histogram_quantile(0.95, sum by (le) (rate(client_ttff_seconds_bucket[$__rate_interval])))", "p95")], "s", legend="list"), 8),
            (ts("Starts by platform and source", [("sum by (platform, source) (increase(client_ttff_seconds_count[$__rate_interval]))", "{{platform}} {{source}}")], "short", legend="list", stack=True, fill=40), 8),
            (ts("Stalls and errors", [("sum by (platform) (increase(client_rebuffer_total[$__rate_interval]))", "stalls {{platform}}"), ("sum by (platform) (increase(client_playback_errors_total[$__rate_interval]))", "errors {{platform}}")], "short", legend="list"), 8))
    d.row("Search")
    d.place(8,
            (ts("Search duration", [("histogram_quantile(0.5, sum by (le) (rate(gateway_search_duration_seconds_bucket[$__rate_interval])))", "p50"), ("histogram_quantile(0.95, sum by (le) (rate(gateway_search_duration_seconds_bucket[$__rate_interval])))", "p95")], "s", legend="list"), 8),
            (ts("Results per search (median)", [("histogram_quantile(0.5, sum by (le, stage) (rate(gateway_search_results_bucket[$__rate_interval])))", "{{stage}}")], "short", legend="list"), 8),
            (ts("Search errors and partial answers", [("sum by (reason) (rate(gateway_search_errors_total[$__rate_interval]))", "error {{reason}}"), ("rate(gateway_search_partial_total[$__rate_interval])", "partial")], "reqps", legend="list"), 8))
    d.place(8,
            (ts("Indexer p95", [("histogram_quantile(0.95, sum by (le, indexer) (rate(gateway_indexer_search_duration_seconds_bucket[$__rate_interval])))", "{{indexer}}")], "s"), 12),
            (ts("TMDB requests", [("sum by (result) (rate(gateway_tmdb_requests_total[$__rate_interval]))", "{{result}}"), ("rate(gateway_tmdb_throttled_total[$__rate_interval])", "throttled")], "reqps", stack=True, fill=40), 12))
    d.row("Logs")
    d.place(10, (logs("Warnings and errors", '{project="media-stack", level=~"warn|warning|error"}'), 24))
    return d


UPMAP = [{"type": "value", "options": {"0": {"text": "DOWN"}, "1": {"text": "UP"}}}]


def main():
    check = "--check" in sys.argv
    stale = []
    for build in (host_containers, stacks, container, windows, media_stack):
        name, text = build().save()
        path = OUT / name
        if check:
            if not path.exists() or path.read_text(encoding="utf-8") != text:
                stale.append(name)
        else:
            path.write_text(text, encoding="utf-8", newline="\n")
    if stale:
        sys.exit("out of date, run build_dashboards.py: " + ", ".join(stale))


if __name__ == "__main__":
    main()
