import { Elysia } from "elysia";
import { cors } from "@elysiajs/cors";
import { authPlugin } from "./auth/middleware";
import { buildMcpServer, streamableTransport } from "./mcp/server";
import { toolRegistry } from "./mcp/tools/registry";
import { config } from "./config";
import { logger, startTelemetry } from "./telemetry";
import { createCheckoutSession } from "./billing/stripe";
import { handleStripeWebhook } from "./billing/webhook";

const { shutdown: shutdownTelemetry } = startTelemetry();

const app = new Elysia()
  .use(cors({ origin: true, credentials: true }))
  .get("/", () => ({
    service: "osint-api",
    version: "0.1.0",
    mcp: "/mcp (POST, GET, DELETE — Streamable HTTP)",
    endpoints: ["/healthz", "/me", "/tools", "/mcp", "/billing/checkout", "/billing/webhook"],
  }))
  .get("/healthz", () => ({ ok: true, service: "osint-api", version: "0.1.0" }))
  .post("/billing/webhook", async ({ request }) => {
    const rawBody = await request.text();
    const sig = request.headers.get("stripe-signature") ?? "";
    await handleStripeWebhook(rawBody, sig);
    return { received: true };
  })
  .use(authPlugin)
  .get("/me", ({ auth }) => ({ uid: auth.user.uid, tenantId: auth.tenantId, userId: auth.userId }))
  .get("/tools", () => ({
    tools: toolRegistry.list().map((t) => ({ name: t.name, description: t.description })),
  }))
  .all("/mcp", async ({ request, auth }) => {
    const transport = streamableTransport();
    const server = buildMcpServer(auth);
    await server.connect(transport);
    return transport.handleRequest(request);
  })
  .post("/billing/checkout", async ({ auth, body, request }) => {
    const { tier } = body as { tier: "hunter" | "operator" };
    const priceId = tier === "operator" ? config.stripe.priceIdOperator : config.stripe.priceIdHunter;
    const base = request.headers.get("origin") ?? `https://${request.headers.get("host")}`;
    return createCheckoutSession({
      tenantId: auth.tenantId,
      userEmail: auth.user.email!,
      priceId,
      successUrl: `${base}/billing/success`,
      cancelUrl: `${base}/billing/cancel`,
    });
  })
  .listen(config.port);

logger.info({ port: config.port }, "osint-api listening");

process.on("SIGTERM", async () => {
  await shutdownTelemetry();
  process.exit(0);
});
