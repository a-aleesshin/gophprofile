import json, pathlib, sys

OUT = pathlib.Path("deploy/helm/gophprofile/files/dashboards")
PROM = {"type": "prometheus", "uid": "prometheus"}


def target(expr, legend, ref="A"):
    return {
        "datasource": PROM,
        "editorMode": "code",
        "expr": expr,
        "legendFormat": legend,
        "range": True,
        "refId": ref,
    }


def panel(title, kind, targets, gw, gh, gx, gy, unit=None, desc=None, extra=None,
          axis=None, legend="right"):
    field = {
        "defaults": {
            "color": {"mode": "palette-classic"},
            "custom": {
                "axisBorderShow": False,
                "drawStyle": "line",
                "fillOpacity": 8,
                "lineWidth": 2,
                "showPoints": "never",
                "spanNulls": True,
            },
            "mappings": [],
            "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": None}]},
        },
        "overrides": [],
    }

    if unit:
        field["defaults"]["unit"] = unit

    if axis:
        field["defaults"].update(axis)

    out = {
        "datasource": PROM,
        "fieldConfig": field,
        "gridPos": {"h": gh, "w": gw, "x": gx, "y": gy},
        "id": None,
        "options": {
            "legend": {"calcs": ["lastNotNull", "max"], "displayMode": "table",
                       "placement": legend, "showLegend": True},
            "tooltip": {"mode": "multi", "sort": "desc"},
        },
        "targets": targets,
        "title": title,
        "type": kind,
    }

    if desc:
        out["description"] = desc

    if kind in ("stat", "gauge"):
        out["options"] = {
            "colorMode": "value",
            "graphMode": "area",
            "justifyMode": "auto",
            "orientation": "auto",
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "textMode": "auto",
        }

    if extra:
        out.update(extra)

    return out


def row(title, gy):
    return {
        "collapsed": False,
        "gridPos": {"h": 1, "w": 24, "x": 0, "y": gy},
        "id": None,
        "panels": [],
        "title": title,
        "type": "row",
    }


SVC = 'service=~"$service"'
OWN = 'job="gophprofile",' + SVC


def variable():
    query = 'label_values(up{job="gophprofile"}, service)'

    return {
        "allValue": ".*",
        "current": {"text": ["All"], "value": ["$__all"]},
        "datasource": PROM,
        "definition": query,
        "includeAll": True,
        "label": "Сервис",
        "multi": True,
        "name": "service",
        "options": [],
        "query": {"qryType": 1, "query": query, "refId": "service"},
        "refresh": 2,
        "regex": "",
        "sort": 1,
        "type": "query",
    }


def dashboard(uid, title, description, panels, tags):
    for index, item in enumerate(panels, start=1):
        item["id"] = index

    return {
        "annotations": {"list": []},
        "description": description,
        "editable": False,
        "graphTooltip": 1,
        "panels": panels,
        "refresh": "10s",
        "schemaVersion": 41,
        "tags": tags,
        "templating": {"list": [variable()]},
        "time": {"from": "now-30m", "to": "now"},
        "timepicker": {},
        "timezone": "browser",
        "title": title,
        "uid": uid,
        "version": 1,
    }


def write(name, doc):
    OUT.mkdir(parents=True, exist_ok=True)
    path = OUT / name
    path.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n")
    print("  ", path, len(json.dumps(doc)), "байт")


HTTP_RATE = (
    'sum by (route) (rate(gophprofile_http_requests_total{%s}[$__rate_interval]))' % SVC
)
HTTP_ERRORS = (
    '(sum(rate(gophprofile_http_requests_total{%s,status=~"5.."}[$__rate_interval]))'
    ' or vector(0))'
    ' / clamp_min(sum(rate(gophprofile_http_requests_total{%s}[$__rate_interval])), 0.0001)'
    % (SVC, SVC)
)
HTTP_ERRORS_BY_ROUTE = (
    'sum by (route) (rate(gophprofile_http_requests_total{%s,status=~"5.."}'
    '[$__rate_interval]))'
    ' or vector(0)' % SVC
)
HTTP_DURATION = 'gophprofile_http_request_duration_seconds'


def quantile(q, metric, by="route"):
    return (
        'histogram_quantile(%s, sum by (le, %s) (rate(%s_bucket{%s}[$__rate_interval])))'
        % (q, by, metric, SVC)
    )


def overall(q, metric):
    return (
        'histogram_quantile(%s, sum by (le) (rate(%s_bucket{%s}[$__rate_interval])))'
        % (q, metric, SVC)
    )


overview = [
    row("Запросы: скорость, ошибки, длительность", 0),
    panel("Запросов в секунду, по маршруту", "timeseries",
          [target(HTTP_RATE, "{{route}}")], 12, 8, 0, 1, unit="reqps",
          desc="R в RED. Маршрут, а не путь: путь несёт идентификаторы."),
    panel("Доля ошибок 5xx", "timeseries",
          [target(HTTP_ERRORS, "5xx")], 6, 8, 12, 1, unit="percentunit",
          axis={"min": 0, "max": 1},
          desc="E в RED. Ось закреплена на 0–100%: иначе при постоянном нуле Grafana "
               "растягивает её до тысяч процентов и панель читается как сломанная. "
               "Знаменатель зажат clamp_min, числитель прикрыт or vector(0)."),
    panel("Ошибок 5xx в секунду, по маршруту", "timeseries",
          [target(HTTP_ERRORS_BY_ROUTE, "{{route}}")], 6, 8, 18, 1, unit="reqps"),
    panel("Длительность по сервису, p50 / p95 / p99", "timeseries",
          [target(overall("0.5", HTTP_DURATION), "p50", "A"),
           target(overall("0.95", HTTP_DURATION), "p95", "B"),
           target(overall("0.99", HTTP_DURATION), "p99", "C")],
          10, 9, 0, 9, unit="s",
          desc="D в RED. Среднего нет намеренно: оно скрывает хвост."),
    panel("Длительность по маршруту, p95", "timeseries",
          [target(quantile("0.95", HTTP_DURATION), "{{route}}")],
          14, 9, 10, 9, unit="s",
          desc="Разделено с панелью слева намеренно: три квантиля на восемь маршрутов "
               "дают 24 серии в одной легенде, и читать её невозможно."),
    row("gRPC между сервисами", 18),
    panel("Вызовов в секунду, по методу и коду", "timeseries",
          [target('sum by (method, code) (rate(gophprofile_grpc_requests_total{%s}'
                  '[$__rate_interval]))' % SVC,
                  "{{method}} {{code}}")], 12, 8, 0, 19, unit="reqps"),
    panel("Длительность gRPC, p95", "timeseries",
          [target(quantile("0.95", "gophprofile_grpc_request_duration_seconds", "method"),
                  "p95 {{method}}")], 12, 8, 12, 19, unit="s"),
]

write("overview.json", dashboard(
    "gophprofile-overview", "GophProfile — обзор сервисов",
    "RED-метрики HTTP и gRPC: скорость, ошибки, длительность.",
    overview, ["gophprofile", "red"]))


resources = [
    row("Пул соединений к Postgres", 0),
    panel("Соединения по состоянию", "timeseries",
          [target('sum by (state) (gophprofile_db_pool_connections{%s})' % SVC,
                   "{{state}}", "A"),
           target('sum(gophprofile_db_pool_connections_max{%s})' % SVC,
                  "предел всех пулов", "B")],
          12, 8, 0, 1,
          desc="Сумма против суммы: слева сложены пулы четырёх процессов, поэтому "
               "и предел берётся суммой, иначе панель показывает мнимое превышение. "
               "Если acquired подходит к пределу, запросы начинают ждать соединения."),
    panel("Ожиданий пула в секунду", "timeseries",
          [target('sum(rate(gophprofile_db_pool_empty_acquires_total{%s}'
                  '[$__rate_interval]))' % SVC,
                  "ждали соединения")], 12, 8, 12, 1,
          desc="Непустое значение — прямой признак, что MAX_CONNS мал."),
    row("Очереди", 9),
    panel("Глубина outbox", "timeseries",
          [target('sum by (transport, state) (gophprofile_outbox_depth{%s})' % SVC,
                  "{{transport}} {{state}}")], 8, 8, 0, 10,
          desc="pending ждёт отправки, parked ждёт человека."),
    panel("Возраст самой старой неотправленной строки", "timeseries",
          [target('max by (transport) '
                  '(gophprofile_outbox_oldest_unpublished_age_seconds{%s})' % SVC,
                  "{{transport}}")], 8, 8, 8, 10, unit="s",
          desc="Главная метрика релея: он может молчать, ничем не падая."),
    panel("Лаг консьюмеров Kafka", "timeseries",
          [target('sum by (group, topic) (gophprofile_kafka_consumer_lag{%s})' % SVC,
                  "{{group}} {{topic}}")], 8, 8, 16, 10),
    row("Доставка сообщений", 18),
    panel("Исходы inbox в секунду", "timeseries",
          [target('sum by (outcome) (rate(gophprofile_inbox_messages_total{%s}'
                  '[$__rate_interval]))' % SVC,
                  "{{outcome}}")], 12, 8, 0, 19, unit="reqps",
          desc="exhausted и dropped — это сообщения, которые никто не применит."),
    panel("Публикации и парковки релея в секунду", "timeseries",
          [target('sum by (transport) (rate(gophprofile_outbox_published_total{%s}'
                  '[$__rate_interval]))' % SVC,
                  "опубликовано {{transport}}", "A"),
           target('sum by (transport) (rate(gophprofile_outbox_publish_failures_total{%s}'
                  '[$__rate_interval])) or vector(0)' % SVC,
                  "сбоев {{transport}}", "B"),
           target('sum by (transport) (rate(gophprofile_outbox_parked_total{%s}'
                  '[$__rate_interval])) or vector(0)' % SVC,
                  "припарковано {{transport}}", "C")],
          12, 8, 12, 19, unit="reqps"),
    row("Процессы", 27),
    panel("Процессор, доля ядра", "timeseries",
          [target('sum by (instance) (rate(process_cpu_seconds_total{%s}'
                  '[$__rate_interval]))' % OWN, "{{instance}}")],
          6, 8, 0, 28, unit="percentunit",
          desc="Единица — одно ядро целиком. Фильтр по job обязателен: без него "
               "в панель попадают сам Prometheus и Alertmanager, у которых нет "
               "метки service, а пустая метка проходит под service=~\".*\"."),
    panel("Память процесса", "timeseries",
          [target('sum by (instance) (process_resident_memory_bytes{%s})' % OWN,
                  "rss {{instance}}", "A"),
           target('sum by (instance) (go_memstats_heap_inuse_bytes{%s})' % OWN,
                  "heap {{instance}}", "B")],
          6, 8, 6, 28, unit="bytes",
          desc="RSS — то, что видит хост и за что убивает OOM killer. Heap — то, "
               "чем распоряжается Go. Разрыв между ними это сам рантайм."),
    panel("Горутины", "timeseries",
          [target('sum by (instance) (go_goroutines{%s})' % OWN, "{{instance}}")],
          6, 8, 12, 28,
          desc="Ровная линия нормальна, растущая без остановки — утечка: каждая "
               "забытая горутина держит свой контекст и свои буферы."),
    panel("Файловые дескрипторы", "timeseries",
          [target('sum by (instance) (process_open_fds{%s})' % OWN,
                  "открыто {{instance}}", "A"),
           target('min(process_max_fds{%s})' % OWN, "предел", "B")],
          6, 8, 18, 28,
          desc="Соединения к БД, брокеру и хранилищу считаются здесь же, так что "
               "приближение к пределу читается раньше, чем первый отказ."),
]

write("resources.json", dashboard(
    "gophprofile-resources", "GophProfile — ресурсы и доставка",
    "Пул Postgres, глубина очередей, лаг Kafka, исходы доставки сообщений.",
    resources, ["gophprofile", "resources"]))


business = [
    row("Загрузки аватарок", 0),
    panel("Готово за 30 минут", "stat",
          [target('round(sum(increase(gophprofile_avatar_uploads_total'
                  '{%s,status="ready"}[30m])))' % SVC,
                  "ready")], 6, 5, 0, 1, axis={"decimals": 0},
          desc="round снаружи намеренно: increase экстраполирует на края окна и "
               "для счётчика событий выдаёт дробь вроде 3.03."),
    panel("Отказов за 30 минут", "stat",
          [target('round(sum(increase(gophprofile_avatar_uploads_total'
                  '{%s,status="failed"}[30m])) or vector(0))' % SVC,
                  "failed")], 6, 5, 6, 1, axis={"decimals": 0},
          desc="or vector(0): отсутствие серии должно читаться как ноль отказов, "
               "а не как «нет данных»."),
    panel("Доля отказов", "stat",
          [target('(sum(rate(gophprofile_avatar_uploads_total{%s,status="failed"}'
                  '[$__rate_interval])) or vector(0))'
                  ' / clamp_min(sum(rate(gophprofile_avatar_uploads_total{%s}'
                  '[$__rate_interval])), 0.0001)' % (SVC, SVC),
                  "доля")], 6, 5, 12, 1, unit="percentunit"),
    panel("Хранится всего", "stat",
          [target('sum(gophprofile_storage_bytes{%s})' % SVC, "байт")],
          6, 5, 18, 1, unit="bytes"),
    panel("Загрузок в секунду, по исходу", "timeseries",
          [target('sum by (status) (rate(gophprofile_avatar_uploads_total{%s}'
                  '[$__rate_interval]))' % SVC,
                  "{{status}}")], 12, 8, 0, 6, unit="reqps"),
    panel("От запроса до готовности, p50 / p95", "timeseries",
          [target(overall("0.5", "gophprofile_avatar_upload_duration_seconds"), "p50", "A"),
           target(overall("0.95", "gophprofile_avatar_upload_duration_seconds"), "p95", "B")],
          12, 8, 12, 6, unit="s",
          desc="Это бизнес-задержка целиком: запрос, брокер, обработка картинки."),
    row("Хранилище и состав", 14),
    panel("Объём по виду объекта", "timeseries",
          [target('sum by (kind) (gophprofile_storage_bytes{%s})' % SVC, "{{kind}}")],
          8, 8, 0, 15, unit="bytes",
          desc="Оригинал считается один раз: его размер повторяется в варианте original."),
    panel("Объектов по виду", "timeseries",
          [target('sum by (kind) (gophprofile_storage_objects{%s})' % SVC, "{{kind}}")],
          8, 8, 8, 15),
    panel("Файлов по статусу", "timeseries",
          [target('sum by (status) (gophprofile_files{%s})' % SVC, "{{status}}")],
          8, 8, 16, 15),
]

write("business.json", dashboard(
    "gophprofile-business", "GophProfile — бизнес-показатели",
    "Загрузки аватарок, задержка до готовности, объём хранилища.",
    business, ["gophprofile", "business"]))
