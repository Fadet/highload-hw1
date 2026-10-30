# Мониторинг Go-микросервиса: Prometheus + Grafana

Учебный проект: CRUD-микросервис на Go с метриками золотых сигналов (latency, traffic, errors),
автоматически генерируемым дашбордом Grafana и генератором нагрузки.

> README дополняется по мере реализации.

## Запуск

```bash
cp .env.example .env    # при желании поменяйте пароль Grafana и порты
docker compose up --build
```

| Сервис | Адрес на хосте | Переменная порта в `.env` |
|---|---|---|
| app-1 (`pod-1`, `dc-a`) | http://localhost:18081 | `APP1_PORT` |
| app-2 (`pod-2`, `dc-b`) | http://localhost:18082 | `APP2_PORT` |
| Prometheus | http://localhost:9090 | `PROMETHEUS_PORT` |
| Grafana | http://localhost:3000 (логин/пароль из `.env`) | `GRAFANA_PORT` |

Остановка: `docker compose down`.

### Секреты

Логин и пароль администратора Grafana задаются в `.env` (`GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD`).
`.env` в `.gitignore`, в репозитории только `.env.example`. В compose переменные объявлены как
`${VAR:?...}`: без `.env` стек не стартует с понятной ошибкой вместо тихого пароля по умолчанию.

### Как устроен стек

```
app-1 ─┐  /metrics (pull каждые 5 с)
app-2 ─┴──────────────▶ Prometheus ◀── PromQL ── Grafana (datasource uid=prometheus)
```

- `app-1` и `app-2` — один образ `highload-hw1-app:local` (собирается только в `app-1`, `app-2` его
  переиспользует), разные `POD_ID`/`DATACENTER`; healthcheck — `/service -healthcheck`.
- Prometheus (`prom/prometheus:v3.13.5`) скрейпит оба экземпляра по `static_configs`
  (`deploy/prometheus/prometheus.yml`), `scrape_interval: 5s`.
- Grafana (`grafana/grafana:13.2.5`) поднимает datasource из
  `deploy/grafana/provisioning/datasources/prometheus.yml` с фиксированным `uid: prometheus` — на него
  ссылается генерируемый дашборд; `editable: false`, так что из UI его не изменить.
- Порядок старта: app → (healthy) → Prometheus → (healthy) → Grafana.

## Микросервис

Хранилище — in-memory (`map` под `sync.RWMutex`).

| Ручка | Описание | Коды ответа |
|---|---|---|
| `POST /items` | создать запись | 201, 400 |
| `GET /items/{id}` | получить запись | 200, 400, 404 |
| `PUT /items/{id}` | обновить запись | 200, 400, 404 |
| `GET /health` | готовность сервиса | 200 |

Тело запроса для `POST`/`PUT`:

```json
{"name": "book", "value": "42"}
```

`name` обязателен, `id` — положительное целое число.

Служебные ручки (метриками не инструментируются):

| Ручка | Описание |
|---|---|
| `GET /health` | 200, когда сервис готов |
| `GET /metrics` | метрики в формате Prometheus |
| `GET /handlers` | список инструментированных ручек: `{"handlers":[{"method":"GET","route":"/items/{id}"}, ...]}` |

## Метрики

Инструментирование — middleware (идиоматичный для Go способ). Бизнес-ручки регистрируются через
`metrics.Router.Handle("GET /items/{id}", h)`: роутер разбирает паттерн на метод и шаблон пути,
оборачивает хендлер в middleware и запоминает ручку для `/handlers`. Поэтому список ручек на дашборде
всегда совпадает с тем, что реально инструментировано, а новая ручка появляется там автоматически.

| Метрика | Тип | Лейблы | Сигнал |
|---|---|---|---|
| `http_request_duration_seconds` | Histogram | `method`, `route`, `pod`, `datacenter` | Latency (p50/p95/p99) |
| `http_requests_total` | Counter | `method`, `route`, `code`, `pod`, `datacenter` | Traffic (RPS) и Errors (`code=~"4..\|5.."`) |

Лейблы:

- `method` + `route` — идентификатор ручки; `route` — **шаблон** пути (`/items/{id}`), а не фактический
  путь, иначе каждый id порождал бы новый временной ряд (взрыв кардинальности);
- `code` — HTTP-код ответа;
- `pod`, `datacenter` — из переменных окружения `POD_ID` и `DATACENTER`; навешиваются на все метрики
  приложения через `prometheus.WrapRegistererWith`.

Отдельного счётчика ошибок нет: ошибки — это подмножество `http_requests_total` с кодом 4xx/5xx,
дублирование привело бы только к лишним рядам.

### Почему Histogram, а не Summary

Summary считает квантили **внутри процесса**, и их нельзя корректно агрегировать: среднее p99 двух подов —
не p99 объединённого трафика. Histogram же экспортирует **счётчики попаданий в бакеты** (`_bucket{le=...}`),
а счётчики складываются. Поэтому перцентиль по любому набору подов и датацентров считается так:

```promql
histogram_quantile(0.99,
  sum by (le) (rate(http_request_duration_seconds_bucket{route="/items/{id}", method="GET"}[1m])))
```

Цена — точность ограничена границами бакетов (внутри бакета значение интерполируется линейно).
Бакеты экспоненциальные: 0.5 мс × 2ⁿ, 14 штук (до ~4 с), — относительная погрешность примерно
одинакова во всём диапазоне.

## Дашборд

JSON дашборда строится в `internal/dashboard` (чистая функция `Build(handlers, opts)` на Go-структурах,
без SDK) по списку ручек, полученному в рантайме из `GET /handlers`. Ручки в коде генератора не
перечислены: новая ручка в приложении после перезапуска стека получит свою строку на дашборде.

- `editable: false` — дашборд нельзя редактировать из UI;
- переменные `datacenter` и `pod` — `label_values(...)`, multi-select + All;
  `pod` зависит от выбранных датацентров: `label_values(http_requests_total{datacenter=~"$datacenter"}, pod)`;
- на каждую ручку — строка (row) `METHOD /route` и три панели.

Селектор каждой панели (пример для `GET /items/{id}`):

```
method="GET", route="/items/{id}", datacenter=~"$datacenter", pod=~"$pod"
```

| Панель | PromQL |
|---|---|
| Latency (p50/p95/p99) | `histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{…}[$__rate_interval])))` |
| RPS | `sum(rate(http_requests_total{…}[$__rate_interval]))` + разбивка `sum by (datacenter)` |
| Errors by code | `sum by (code) (rate(http_requests_total{…, code=~"4..\|5.."}[$__rate_interval]))` |

Почему так:

- `rate` считается **до** `sum`: `rate` корректно обрабатывает сбросы счётчика при рестарте пода, а сумма
  сброшенных счётчиков дала бы ложный провал;
- для перцентилей суммируются бакеты по `le` (все остальные лейблы схлопываются), и только потом
  `histogram_quantile` — так перцентиль считается по объединённому распределению выбранных подов;
- `=~` нужен, потому что при multi-select/All Grafana подставляет в переменную регэксп `(dc-a|dc-b)`;
- `$__rate_interval` — окно, которое Grafana подбирает не меньше 4× scrape interval, чтобы в `rate`
  всегда попадало достаточно точек.

### Генератор дашборда (`cmd/dashgen`)

Одноразовый контейнер в compose. Стартует, когда `app-1`, `app-2` и `grafana` прошли healthcheck
(`depends_on: condition: service_healthy`), и завершается с кодом 0 после загрузки дашборда.

1. `GET /handlers` у **каждого** экземпляра из `APP_URLS`, списки объединяются без дублей
   (если на подах разные версии приложения, на дашборде будут все ручки).
2. `dashboard.Build(...)` → `POST /api/dashboards/db` с basic auth администратора и `overwrite: true`:
   повторный запуск обновляет тот же дашборд (фиксированный `uid`), а не создаёт копию.
3. Сетевые ошибки и 5xx — повтор с экспоненциальной задержкой до `TIMEOUT`; 4xx (например, неверный
   пароль) — сразу ошибка, повторять бессмысленно.

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `APP_URLS` | `http://app-1:8080` | экземпляры приложения через запятую |
| `GRAFANA_URL` | `http://grafana:3000` | адрес Grafana |
| `GRAFANA_USER`, `GRAFANA_PASSWORD` | — (обязательны) | учётные данные администратора, в compose берутся из `.env` |
| `DATASOURCE_UID` | `prometheus` | uid datasource из provisioning |
| `DASHBOARD_UID`, `DASHBOARD_TITLE` | `items-service`, `Items service: golden signals` | идентификатор и заголовок дашборда |
| `TIMEOUT` | `2m` | общий таймаут с учётом повторов |

Дашборд: http://localhost:3000/d/items-service

Перегенерировать без перезапуска стека: `docker compose run --rm dashgen`.

## Локальный запуск без Docker

```bash
go test ./...
POD_ID=pod-1 DATACENTER=dc-a go run ./cmd/app   # слушает :8080, адрес меняется переменной ADDR
```

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `ADDR` | `:8080` | адрес HTTP-сервера |
| `POD_ID` | `local` | лейбл `pod` |
| `DATACENTER` | `local` | лейбл `datacenter` |
| `FAILURE_RATE` | `0` | вероятность (0..1) искусственного ответа 500 на бизнес-ручках |
| `LATENCY_MEAN` | `0` | средняя искусственная задержка (`20ms`), распределение экспоненциальное, максимум 2 с |

### Искусственные сбои и задержка

Бизнес-хендлеры обёрнуты в слой `chaos` (`internal/api`), который стоит **внутри** metrics-middleware,
поэтому метрики видят искусственные 500 и задержки как настоящие:

```
запрос → metrics middleware → chaos (sleep, 500 с вероятностью FAILURE_RATE) → хендлер
```

- 500 — от `FAILURE_RATE`;
- 404 — естественные, на запросы к несуществующим id (их генерирует нагрузочный инструмент).

Задержка распределена экспоненциально: без неё in-memory ответ занимает микросекунды, все запросы
попадают в первый бакет, и p50/p95/p99 сливаются в одну линию. При `LATENCY_MEAN=20ms` теоретически
p50 ≈ 14 мс, p95 ≈ 60 мс, p99 ≈ 92 мс.

## Docker

Один `Dockerfile` для всех бинарников: multi-stage сборка (`golang:1.25.1-alpine` → статический бинарник
с `CGO_ENABLED=0`) и минимальный runtime-образ `gcr.io/distroless/static-debian12:nonroot` (~18 МБ,
без shell, не root). Какой бинарник собрать, задаёт build-arg `CMD` (по умолчанию `app`).

```bash
docker build -t highload-hw1-app .
docker run --rm -p 18080:8080 -e POD_ID=pod-1 -e DATACENTER=dc-a -e FAILURE_RATE=0.05 -e LATENCY_MEAN=20ms highload-hw1-app
```

В образе нет `curl`/`wget`, поэтому healthcheck выполняет сам бинарник: `/service -healthcheck` делает
`GET http://127.0.0.1:<порт>/health` и возвращает код выхода 0/1. Healthcheck нужно задавать
в exec-форме (`["CMD", "/service", "-healthcheck"]`) — shell-форма в distroless не работает.

## Структура

```
cmd/app/            точка входа сервиса
cmd/dashgen/        генератор дашборда (CLI)
internal/store/     in-memory хранилище
internal/api/       HTTP-хендлеры
internal/metrics/   middleware с метриками, список ручек
internal/dashboard/ построение JSON дашборда Grafana
deploy/             конфиги Prometheus и provisioning Grafana
```
