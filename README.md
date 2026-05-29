# demo-otel-tracing

A proof-of-concept for **distributed tracing** using [OpenTelemetry](https://opentelemetry.io/), with traces visualised in Grafana Tempo.

Two scenarios are demonstrated:

| Scenario | Strategy | Transport |
|---|---|---|
| HTTP call: Go → Python | W3C `traceparent` header (auto-propagated) | HTTP |
| Pub/Sub: Go → PHP | **Strategy #1 — W3C child span via message attributes** | Redis Streams |

## Architecture

```
                         ┌─────────────────────────────────────┐
curl /test-tracing ──▶   │  golang-service :8000               │
                         │    └── HTTP call w/ traceparent ──▶ │  python-service :8001
                         └─────────────────────────────────────┘

                         ┌─────────────────────────────────────┐
curl /test-pubsub ──▶    │  golang-service :8000               │
                         │    └── XADD demo-stream             │
                         │         {traceparent, payload} ───▶ │  redis :6379
                         └─────────────────────────────────────┘
                                                                      │
                                                              php-service (consumer)
                                                                reads traceparent,
                                                                creates child span

All services export spans via OTLP:
  gRPC (Go, Python) → otel-collector :4317 → tempo :3200 → grafana :3000
  HTTP (PHP)        → otel-collector :4318 ↗
```

## Services

| Service | Language | Role |
|---|---|---|
| `golang-service` | Go (net/http) | Publisher — HTTP + Redis Stream |
| `python-service` | Python (FastAPI) | HTTP downstream |
| `php-service` | PHP 8.2 (CLI) | Redis Stream consumer |
| `redis` | Redis 7 | Message broker (Redis Streams) |
| `otel-collector` | OTel Collector | Receives & forwards spans |
| `tempo` | Grafana Tempo | Trace storage |
| `grafana` | Grafana | Trace visualisation |

## Tracing strategies

### Strategy #1 — W3C child span (pub/sub demo)

The Go publisher injects the current `traceparent` (and `baggage`) into the Redis Stream message fields before publishing. The PHP consumer extracts them and starts its own span with that context as the parent:

```
GET /test-pubsub
  └── [golang-service] pubsub.publish   (traceId: abc, spanId: 111)
          │  publishes {traceparent: "00-abc-111-01", payload: ...}
          ▼ (async via Redis Stream)
      [php-service] pubsub.consume      (traceId: abc, spanId: 222, parentId: 111)
```

Result: **one continuous trace waterfall** across Go and PHP in Grafana Tempo.

### HTTP propagation (existing demo)

`otelhttp.NewTransport` automatically injects `traceparent` into outbound HTTP headers, linking the Go and Python spans under the same trace.

## How to run

**Prerequisites:** Docker + Docker Compose

```bash
docker compose up --build
```

### Trigger traces

| Command | What it demonstrates |
|---|---|
| `curl http://localhost:8000/test-tracing` | HTTP distributed trace: Go → Python |
| `curl http://localhost:8000/test-pubsub` | Pub/Sub distributed trace: Go → Redis → PHP |

## Viewing traces in Grafana

1. Open http://localhost:3000 and log in with `admin` / `admin`
2. Go to **Explore** → select the **Tempo** datasource
3. Use **Search** tab → filter by `Service Name`
4. Click a trace to see the full span waterfall

For the pub/sub trace, look for a trace that spans both `golang-service` (span: `pubsub.publish`) and `php-service` (span: `pubsub.consume`) — they share the same `traceId`.

## Project structure

```
demo-otel-tracing/
├── golang-service/       # Go publisher (HTTP + Redis Stream)
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
├── python-service/       # Python HTTP downstream
│   ├── main.py
│   ├── requirements.txt
│   └── Dockerfile
├── php-service/          # PHP Redis Stream consumer (Strategy #1)
│   ├── consumer.php
│   ├── composer.json
│   └── Dockerfile
├── otel-collector/
│   └── config.yaml       # Receives spans (gRPC :4317, HTTP :4318), forwards to Tempo
├── tempo/
│   └── config.yaml       # Trace storage backend
├── grafana/
│   └── provisioning/
│       └── datasources/
│           └── tempo.yaml # Auto-configures Tempo datasource
├── tracing-strategies-docs.md  # Reference: all 6 tracing strategies
└── docker-compose.yml
```
