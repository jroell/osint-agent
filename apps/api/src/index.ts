import { Elysia } from "elysia";
import { cors } from "@elysiajs/cors";
import { authPlugin } from "./auth/middleware";
import { buildMcpServer, streamableTransport } from "./mcp/server";
import { toolRegistry } from "./mcp/tools/registry";
import { config } from "./config";
import { logger, startTelemetry } from "./telemetry";

const { shutdown: shutdownTelemetry } = startTelemetry();

const app = new Elysia()
  .use(cors({ origin: true, credentials: true }))
  .get("/healthz", () => ({ ok: true, service: "osint-api", version: "0.1.0" }))
  .use(authPlugin)
  .get("/me", ({ auth }) => ({ uid: auth.user.uid, tenantId: auth.tenantId, userId: auth.userId }))
  .get("/tools", () => ({
    tools: toolRegistry.list().map((t) => ({ name: t.name, description: t.description })),
  }))
  .post("/mcp", async ({ request, auth }) => {
    const transport = streamableTransport();
    const server = buildMcpServer(auth);
    await server.connect(transport);
    // @ts-ignore — streamable HTTP expects Node req/res; Bun adapter will handle
    return transport.handleRequest(request);
  })
  .listen(config.port);

logger.info({ port: config.port }, "osint-api listening");

process.on("SIGTERM", async () => {
  await shutdownTelemetry();
  process.exit(0);
});
