import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Apex domain (e.g. 'vurvey.app')"),
});

toolRegistry.register({
  name: "alienvault_otx_passive_dns",
  description:
    "**Fourth moat-feeding discovery channel — passive DNS.** Queries AlienVault OTX (LevelBlue Labs) for passive DNS observations on a target domain. Passive DNS is recorded by sensors across the internet — these are real-world DNS lookups that actually resolved, not what's currently advertised. Uniquely exposes: subdomains never published (internal tooling, staging, private dashboards), historical IP infrastructure that may still host legacy services, and traffic patterns the org didn't intend to leak. Different visibility model from `js_endpoint_extract` (web-source), `swagger_openapi_finder` (advertised specs), and `wayback_endpoint_extract` (web archive) — passive DNS sees the SHADOW of real-world traffic. Free tier (10,000 req/hr with key) — sign up at https://otx.alienvault.com. REQUIRES OTX_API_KEY.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "alienvault_otx_passive_dns",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "alienvault_otx_passive_dns failed");
    return res.output;
  },
});
