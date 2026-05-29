package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// rdb is the shared Redis client used by the pub/sub handler.
var rdb *redis.Client

func initRedis() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "redis:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: addr})
}

func initTracer(ctx context.Context) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "otel-collector:4317"
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("golang-service"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// testPubSubHandler publishes a message to a Redis Stream, injecting the
// current W3C traceparent + baggage into the message attributes (Strategy #1).
// The PHP consumer extracts those attributes and creates a child span, forming
// one continuous trace waterfall across both services.
func testPubSubHandler(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer("golang-service")
	ctx, span := tracer.Start(r.Context(), "pubsub.publish")
	defer span.End()

	// Attach baggage so it travels with the trace context.
	m1, _ := baggage.NewMember("user.id", "42")
	bag, _ := baggage.New(m1)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	// Strategy #1 — inject traceparent + baggage into a plain map carrier.
	// The PHP consumer will call TraceContextPropagator::extract($fields) on
	// these same keys to restore the parent context.
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	// Build Redis Stream message: OTel keys + application payload.
	fields := map[string]interface{}{
		"payload": `{"event":"test-pubsub","data":"hello from golang-service"}`,
	}
	for k, v := range carrier {
		fields[k] = v
	}

	msgID, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "demo-stream",
		Values: fields,
	}).Result()
	if err != nil {
		http.Error(w, fmt.Sprintf("publish failed: %v", err), http.StatusInternalServerError)
		return
	}

	span.SetAttributes(
		attribute.String("messaging.system", "redis"),
		attribute.String("messaging.destination", "demo-stream"),
		attribute.String("messaging.message_id", msgID),
		attribute.String("messaging.operation", "publish"),
	)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w,
		`{"service":"golang-service","stream":"demo-stream","message_id":%q,"strategy":"#1 W3C child span","traceparent":%q}`,
		msgID, carrier["traceparent"],
	)
}

func testTracingHandler(w http.ResponseWriter, r *http.Request) {
	pythonURL := os.Getenv("PYTHON_SERVICE_URL")
	if pythonURL == "" {
		pythonURL = "http://python-service:8001"
	}

	// otelhttp.NewTransport propagates the trace context via W3C headers
	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	m1, _ := baggage.NewMember("user.id", "42")
	bag, _ := baggage.New(m1)
	ctx := baggage.ContextWithBaggage(r.Context(), bag)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pythonURL+"/internal-tracing", nil)
	if err != nil {
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream call failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"service":"golang-service","python_response":%s}`, string(body))
}

func main() {
	ctx := context.Background()

	initRedis()

	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("failed to initialise tracer: %v", err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("tracer shutdown error: %v", err)
		}
	}()

	mux := http.NewServeMux()
	// Wrap the handler with otelhttp so every request gets a server span
	mux.Handle("/test-tracing", otelhttp.NewHandler(
		http.HandlerFunc(testTracingHandler),
		"GET /test-tracing",
	))
	mux.Handle("/test-pubsub", otelhttp.NewHandler(
		http.HandlerFunc(testPubSubHandler),
		"GET /test-pubsub",
	))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("golang-service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
