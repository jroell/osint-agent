import { NodeSDK } from "@opentelemetry/sdk-node";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { Resource } from "@opentelemetry/resources";
import { SEMRESATTRS_SERVICE_NAME, SEMRESATTRS_SERVICE_VERSION } from "@opentelemetry/semantic-conventions";
import pino from "pino";

export const logger = pino({
  level: process.env.LOG_LEVEL ?? "info",
  transport: process.env.NODE_ENV === "development" ? { target: "pino-pretty" } : undefined,
});

export function startTelemetry(): { shutdown: () => Promise<void> } {
  const endpoint = process.env.OTEL_EXPORTER_OTLP_ENDPOINT;
  if (!endpoint) {
    logger.warn("OTEL_EXPORTER_OTLP_ENDPOINT not set — telemetry disabled");
    return { shutdown: async () => {} };
  }

  const headers: Record<string, string> = {};
  const rawHeaders = process.env.OTEL_EXPORTER_OTLP_HEADERS;
  if (rawHeaders) {
    for (const kv of rawHeaders.split(",")) {
      const [k, v] = kv.split("=");
      if (k && v) headers[decodeURIComponent(k.trim())] = decodeURIComponent(v.trim());
    }
  }

  const sdk = new NodeSDK({
    resource: new Resource({
      [SEMRESATTRS_SERVICE_NAME]: process.env.OTEL_SERVICE_NAME ?? "osint-api",
      [SEMRESATTRS_SERVICE_VERSION]: "0.1.0",
    }),
    traceExporter: new OTLPTraceExporter({ url: `${endpoint}/v1/traces`, headers }),
    instrumentations: [getNodeAutoInstrumentations()],
  });

  sdk.start();
  logger.info("OpenTelemetry started");

  return { shutdown: () => sdk.shutdown() };
}
