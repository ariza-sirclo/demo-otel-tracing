## Distributed Tracing Strategies in Event-Driven Systems

---

### 1. Context Propagation via Message Attributes (W3C TraceContext)

Inject `traceparent` + `baggage` into message metadata. Consumer extracts and creates a **child span**.

```
Producer [span A] → message{traceparent: "00-traceId-spanA-01"} → Consumer [span B, parent=A]
```

**Pros:**
- Single continuous trace — full end-to-end visibility in one waterfall
- Standard (W3C), works across languages/frameworks
- Baggage (user.id, tenant.id, etc.) travels with message

**Cons:**
- Tight coupling: consumer must "know" about producer's trace
- If message is reprocessed/retried, span lineage gets confusing
- Dead letter queues break the chain

---

### 2. Span Links (async relationship)

Extract producer context but attach it as a **link**, not a parent. Consumer span has its own root trace.

```
Producer [trace-1: span A] ──link──→ Consumer [trace-2: span B]
```

**Pros:**
- Semantically correct for async — no false parent-child implied latency
- Consumer trace is independently readable
- Handles fan-out (one message → many consumers) cleanly
- Handles retry/replay without corrupting lineage

**Cons:**
- Most UIs (Tempo, Jaeger) have poor link visualization — you have to click through
- Requires manual implementation; no auto-instrumentation supports it well yet

---

### 3. Correlation ID Only (no OTel propagation)

Generate a UUID (`correlationId`) at the origin, pass it in message attributes. Each service creates its own independent trace but tags spans with it.

```
Producer [trace-1, correlationId=abc] → Consumer [trace-2, correlationId=abc]
```

Query by `correlationId` across multiple traces in your backend.

**Pros:**
- Simple — no OTel propagation complexity
- Survives retries, replays, fan-out naturally
- Works even with non-OTel services (just pass a string)
- Easy to query in Loki/Tempo by attribute

**Cons:**
- Not a native OTel concept — requires custom query discipline
- No automatic trace waterfall — you manually stitch traces together
- Latency gaps not visible in a single view

---

### 4. Trace Per Message (full isolation)

Each consumer starts a completely fresh trace with no link to producer.

**Pros:**
- Zero coupling between services
- Simple implementation (just... do nothing special)
- Retry/replay is clean

**Cons:**
- Zero end-to-end visibility — you can't connect what produced what
- Debugging cross-service issues requires correlating by time/payload manually
- Generally considered an anti-pattern for observable systems

---

### 5. Saga / Workflow Tracing (dedicated orchestration span)

A long-running saga or workflow creates a **root span** that all steps attach to, passed through all messages as the parent.

```
[saga-root span] → [step-1 span] → [step-2 span] → [step-3 span]
        └── all linked under one trace waterfall
```

**Pros:**
- Best for business process visibility (order flow, payment saga, etc.)
- One trace = one business transaction, regardless of how many hops
- Clear SLA monitoring per saga

**Cons:**
- Root span must stay open until saga completes (can be hours/days) — unusual
- Or use span links pointing back to a "saga context" trace ID stored elsewhere
- Complex to implement correctly

---

### 6. Exemplar + Metrics Correlation

Don't propagate trace context at all. Instead, emit metrics with trace ID as an **exemplar** (Prometheus exemplars), and correlate via Grafana Tempo + Mimir.

**Pros:**
- No coupling in messaging layer
- Powerful for high-volume systems where sampling means you can't trace everything
- Works well when combined with strategy 3 (correlationId)

**Cons:**
- Not true distributed tracing — it's trace-assisted metrics
- Requires Prometheus exemplar support + Grafana stack
- Only useful if you're already doing metrics-first observability

---

## Comparison Table

| Strategy | End-to-end trace | Retry-safe | Fan-out safe | Implementation effort | UI support |
|---|---|---|---|---|---|
| Child span (W3C) | ✅ one waterfall | ⚠️ messy | ⚠️ messy | Low | Excellent |
| Span links | ✅ linked traces | ✅ | ✅ | Medium | Poor |
| Correlation ID | ⚠️ manual stitch | ✅ | ✅ | Low | Manual query |
| Full isolation | ❌ | ✅ | ✅ | None | N/A |
| Saga root span | ✅ one waterfall | ✅ | ✅ | High | Good |
| Exemplars | ⚠️ indirect | ✅ | ✅ | Medium | Grafana only |

---

## Most Common in Practice

**Simple microservices (Kafka/Pub/Sub, low retry rate):**
> **Strategy 1 (W3C child span)** — default choice. Most libraries auto-instrument it (OpenTelemetry Kafka, Spring Cloud Sleuth, etc.)

**High-reliability systems with retries/DLQ:**
> **Strategy 2 (span links) + Strategy 3 (correlationId)** combined. Link for tracing, correlationId for querying.

**Business process / sagas:**
> **Strategy 5 (saga root)** — store the saga trace ID in your saga state store, re-attach at each step.

**High-volume / sampling-heavy systems:**
> **Strategy 3 (correlationId) + Strategy 6 (exemplars)** — trace a sample, correlate the rest via ID.