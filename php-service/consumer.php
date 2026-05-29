<?php
declare(strict_types=1);

/**
 * PHP pub/sub consumer — Strategy #1: W3C TraceContext child span
 *
 * The Go publisher injects `traceparent` (+ `baggage`) into the Redis Stream
 * message attributes before publishing.  This consumer extracts that context
 * and starts its own span as a *child* of the producer span, producing a
 * single continuous trace waterfall across both services in Grafana Tempo.
 *
 * Strategy #1 recap:
 *   Producer [span A]
 *       └── Consumer [span B]  ← child, same traceId, parentId = A
 */

require_once __DIR__ . '/vendor/autoload.php';

use GuzzleHttp\Client as GuzzleClient;
use GuzzleHttp\Psr7\HttpFactory;
use Http\Adapter\Guzzle7\Client as GuzzlePsrAdapter;
use OpenTelemetry\API\Trace\Propagation\TraceContextPropagator;
use OpenTelemetry\API\Trace\SpanKind;
use OpenTelemetry\Contrib\Otlp\SpanExporter;
use OpenTelemetry\SDK\Common\Attribute\Attributes;
use OpenTelemetry\SDK\Common\Export\Http\PsrTransportFactory;
use OpenTelemetry\SDK\Resource\ResourceInfo;
use OpenTelemetry\SDK\Trace\TracerProvider;
use OpenTelemetry\SDK\Trace\SpanProcessor\SimpleSpanProcessor;
use OpenTelemetry\SemConv\ResourceAttributes;

// ── Tracer setup ─────────────────────────────────────────────────────────────

$otlpBase = rtrim(getenv('OTEL_EXPORTER_OTLP_ENDPOINT') ?: 'http://otel-collector:4318', '/');
$endpoint = $otlpBase . '/v1/traces';

// Explicit PSR-18 client + PSR-17 factories — no auto-discovery magic
$httpClient = new GuzzlePsrAdapter(new GuzzleClient(['timeout' => 5]));
$psr17      = new HttpFactory();

$transport = (new PsrTransportFactory($httpClient, $psr17, $psr17))
    ->create($endpoint, 'application/json');

$resource = ResourceInfo::create(Attributes::create([
    ResourceAttributes::SERVICE_NAME    => 'php-service',
    ResourceAttributes::SERVICE_VERSION => '1.0.0',
]));

$tracerProvider = new TracerProvider(
    new SimpleSpanProcessor(new SpanExporter($transport)),
    null,
    $resource,
);

$tracer = $tracerProvider->getTracer('php-service');

// W3C TraceContext propagator — reads `traceparent` (and `tracestate`) keys
$propagator = TraceContextPropagator::getInstance();

// ── Redis setup (phpredis extension) ─────────────────────────────────────────

$redisHost = getenv('REDIS_HOST') ?: 'redis';

$redis = new Redis();
// Retry connection a few times so the container can start while redis is warming up
for ($i = 0; $i < 10; $i++) {
    try {
        $redis->connect($redisHost, 6379);
        break;
    } catch (RedisException $e) {
        echo "Redis not ready, retrying in 2s... ({$e->getMessage()})\n";
        sleep(2);
    }
}

$lastId = '0-0'; // start from the beginning of the stream on fresh boot
echo "php-service consumer started — listening on 'demo-stream' (Strategy #1: W3C child span)\n";

// ── Consume loop ──────────────────────────────────────────────────────────────

while (true) {
    // Block up to 2 s waiting for new messages; returns false on timeout.
    // phpredis xRead: (array $streams, int $count, int $blockMs)
    $response = $redis->xRead(['demo-stream' => $lastId], 10, 2_000);

    if (empty($response)) {
        continue;
    }

    foreach ($response as $stream => $messages) {
        foreach ($messages as $id => $fields) {
            echo "Received [{$id}] on '{$stream}'\n";

            // ── Strategy #1 ──────────────────────────────────────────────────
            // Extract the W3C traceparent (injected by the Go publisher) from
            // the message fields.  The resulting context carries the producer's
            // traceId and spanId, so the span we create below becomes a CHILD
            // of the producer span — one continuous trace waterfall.
            $parentContext = $propagator->extract($fields);

            $span = $tracer
                ->spanBuilder('pubsub.consume')
                ->setParent($parentContext)
                ->setSpanKind(SpanKind::KIND_CONSUMER)
                ->startSpan();

            $scope = $span->activate();
            try {
                $span->setAttribute('messaging.system',      'redis');
                $span->setAttribute('messaging.destination', 'demo-stream');
                $span->setAttribute('messaging.message_id',  $id);
                $span->setAttribute('messaging.operation',   'receive');

                $payload = $fields['payload'] ?? '{}';
                echo "  payload  : {$payload}\n";
                echo "  parent   : " . ($fields['traceparent'] ?? '(none)') . "\n";

                // Simulate processing work
                usleep(30_000); // 30 ms

                echo "  processed: {$id}\n";
            } catch (\Throwable $e) {
                $span->recordException($e);
                echo "  ERROR: {$e->getMessage()}\n";
            } finally {
                $scope->detach();
                $span->end(); // exports immediately via SimpleSpanProcessor
            }

            $lastId = $id; // advance cursor so we don't re-read
        }
    }
}
