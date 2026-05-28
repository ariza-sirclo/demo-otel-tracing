import os
import logging

from fastapi import FastAPI, Request
import uvicorn

from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource, SERVICE_NAME
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.baggage import get_all, get_baggage

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def init_tracer() -> None:
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317")

    resource = Resource(attributes={SERVICE_NAME: "python-service"})

    exporter = OTLPSpanExporter(endpoint=endpoint, insecure=True)

    provider = TracerProvider(resource=resource)
    provider.add_span_processor(BatchSpanProcessor(exporter))

    trace.set_tracer_provider(provider)

    logger.info("OTel tracer initialised → %s", endpoint)


init_tracer()

app = FastAPI(title="python-service")
FastAPIInstrumentor.instrument_app(app)


@app.get("/internal-tracing")
async def internal_tracing(request: Request):
    print(dict(request.headers)) 
    tracer = trace.get_tracer(__name__)
    with tracer.start_as_current_span("internal-tracing-work") as span:
        span.set_attribute("custom.attribute", "hello-from-python")
        user_id = get_baggage("user.id")
        return {"service": "python-service", "message": "internal tracing endpoint reached", "user_id": user_id}


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 8001))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")
