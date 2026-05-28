# demo-otel-tracing

A proof-of-concept for **distributed tracing** using [OpenTelemetry](https://opentelemetry.io/), with traces visualised in Grafana Tempo.

## Architecture

```
curl → golang-service :8000/test-tracing
             └── calls → python-service :8001/internal-tracing
                              └── spans exported via gRPC
                                       ↓
                            otel-collector :4317
                                       ↓
                                  tempo :3200
                                       ↓
                               grafana :3000
```

| Service | Language | Endpoint |
|---|---|---|
| golang-service | Go (net/http) | `GET /test-tracing` |
| python-service | Python (FastAPI) | `GET /internal-tracing` |

Trace context is propagated between services using the **W3C `traceparent` header**, so both spans appear under the same trace in Tempo.

## How to run

**Prerequisites:** Docker + Docker Compose

```bash
docker compose up --build
```

Services will be available at:

| URL | Description |
|---|---|
| http://localhost:8000/test-tracing | Trigger a distributed trace |
| http://localhost:3000 | Grafana UI (admin / admin) |

## Viewing traces in Grafana

1. Open http://localhost:3000 and log in with `admin` / `admin`
2. Go to **Explore** → select the **Tempo** datasource
3. Use **Search** tab → filter by `Service Name`: `golang-service` or `python-service`
4. Click a trace to see the full span tree across both services

## Project structure

```
demo-otel-tracing/
├── golang-service/       # Go service
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
├── python-service/       # Python service
│   ├── main.py
│   ├── requirements.txt
│   └── Dockerfile
├── otel-collector/
│   └── config.yaml       # Receives spans, forwards to Tempo
├── tempo/
│   └── config.yaml       # Trace storage backend
├── grafana/
│   └── provisioning/
│       └── datasources/
│           └── tempo.yaml # Auto-configures Tempo datasource
└── docker-compose.yml
```
