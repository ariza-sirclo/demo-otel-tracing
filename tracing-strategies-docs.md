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

---

## Strategy #1 vs #5 — Detailed Comparison

They share the same TraceID from start to finish, but differ in **span hierarchy** and **intent**.

### #1 — Linear chain, each step is child of the previous

```
[span A: producer]
    └── [span B: consumer-1]
            └── [span C: consumer-2]
```

- Span A closes when the producer finishes publishing
- Each span is child of the **previous** span — a chain
- The root span (A) is already **closed** by the time consumers run
- Fan-out becomes awkward nesting

### #5 — Star topology, all steps are children of one saga root

```
[span ROOT: saga-create-order]  ← persisted in DB/Redis for entire saga lifetime
    ├── [span A: payment-step]
    ├── [span B: inventory-step]   ← siblings, not a chain
    └── [span C: shipping-step]
```

- A dedicated root span is created to represent the whole business transaction
- Root context is **persisted** in a saga state store, not just passed in a message
- All steps — regardless of timing, order, or service — attach as **siblings of ROOT**
- Fan-out is natural; retries don't corrupt lineage

| | Strategy 1 | Strategy 5 |
|---|---|---|
| Hierarchy | Chain: A → B → C | Star: root → A, B, C |
| Root span owner | First producer (closes early) | Saga orchestrator (spans whole flow) |
| Fan-out | Awkward nested children | Natural siblings |
| Root span lifetime | Short (producer request) | Long (entire saga duration) |
| Context storage | Message attribute only | Message attribute + **persisted in DB** |
| Use case | Simple pipeline, one path | Business transaction with branching/parallel steps |

---

## Recommendations for A → B → C + Master Data Broadcast

### Primary pipeline: A → Pub/Sub → B → Pub/Sub → C

Use **Strategy #1 (W3C child span)**.

```
[span A: service-A]
    └── [span B: service-B]
            └── [span C: service-C]
```

Inject `traceparent` + `baggage` into Pub/Sub message attributes. Each consumer extracts and creates a child span.

**Go publisher:**
```go
carrier := propagation.MapCarrier{}
otel.GetTextMapPropagator().Inject(ctx, carrier)
msg := &pubsub.Message{
    Data:       payload,
    Attributes: carrier, // carries traceparent + baggage
}
topic.Publish(ctx, msg)
```

**Python consumer:**
```python
ctx = propagate.extract(dict(message.attributes))
with tracer.start_as_current_span("pubsub-consume", context=ctx) as span:
    user_id = get_baggage("user.id")  # baggage also restored
```

### Master data broadcast: 1 topic → many consumers

Use **Strategy #2 (span links)**.

```
Producer [trace-1: span A] ──link──→ Consumer-X [trace-2: span B]
                           ──link──→ Consumer-Y [trace-3: span C]
                           ──link──→ Consumer-Z [trace-4: span D]
```

Each consumer is independent — span links let you navigate from producer to any consumer without false parent-child latency implications.

---

## Migration Path: #1 → #5 (when needed)

The migration is **additive and non-breaking** — no flag day required.

### Step 1: Add `x-root-traceparent` message attribute

```
Message Attributes (today, #1):
  traceparent: "00-traceId-spanB-01"   ← changes at every hop

Message Attributes (after, hybrid #1+#5):
  traceparent: "00-traceId-spanB-01"   ← still changes (chain visibility)
  x-root-traceparent: "00-traceId-spanA-01"  ← NEW: never changes, always = origin
```

### Step 2: Publisher seeds it once

The **first service** that initiates the transaction sets `x-root-traceparent` if not already present:

```go
if _, exists := msg.Attributes["x-root-traceparent"]; !exists {
    rootCarrier := propagation.MapCarrier{}
    otel.GetTextMapPropagator().Inject(ctx, rootCarrier)
    msg.Attributes["x-root-traceparent"] = rootCarrier["traceparent"]
}
```

### Step 3: Consumers attach to root instead of previous span

```go
// Before (#1): child of whoever published
ctx := propagator.Extract(ctx, messageAttrs)

// After (#5): child of original root
rootCarrier := MapCarrier{"traceparent": msg.Attributes["x-root-traceparent"]}
rootCtx := propagator.Extract(context.Background(), rootCarrier)
span := tracer.Start(rootCtx, "process-message")

// Pass root through unchanged when publishing downstream
outMsg.Attributes["x-root-traceparent"] = msg.Attributes["x-root-traceparent"]
```

### Rollout

- Services that don't yet read `x-root-traceparent` continue working with #1 behavior
- Migrate services one by one
- No downtime, no coordination required