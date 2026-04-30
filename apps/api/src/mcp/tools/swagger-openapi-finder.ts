import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Target URL or bare domain (e.g. 'vurvey.app' or 'https://api.example.com')"),
  concurrency: z.number().int().min(1).max(50).default(10),
});

toolRegistry.register({
  name: "swagger_openapi_finder",
  description:
    "Probe ~35 well-known paths for exposed OpenAPI/Swagger spec documents (/swagger.json, /openapi.json, /v2/api-docs, /v3/api-docs, /swagger-ui.html, /api/docs, /.well-known/openapi.json, etc.). For each found spec, parses the full document and extracts every operation with method+path+parameters+auth-requirements+response-codes. The fastest path from 'we found an API endpoint' to 'we have the entire machine-readable API surface'. Returns a `high_risk_unauthed_ops` list flagging operations with NO auth requirement on suspicious paths (admin/users/internal/payment/etc) — direct bug-bounty submission candidates. No API key required.",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "swagger_openapi_finder",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "swagger_openapi_finder failed");
    return res.output;
  },
});
