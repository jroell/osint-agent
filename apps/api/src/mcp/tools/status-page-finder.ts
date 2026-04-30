import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(2).describe("Apex domain (e.g. 'anthropic.com') or brand name"),
  additional_urls: z.array(z.string()).optional().describe("Custom status page URLs to probe"),
});

toolRegistry.register({
  name: "status_page_finder",
  description:
    "**Status page = microservice topology + live ops intel.** Probes status.<domain>, healthcheck.<domain>, plus known SaaS vendor patterns (.statuspage.io, .instatus.com, .betterstack.com). For Statuspage.io family: parses official /api/v2/summary.json + /api/v2/incidents.json — returns service component list (microservice topology), active incidents (current ops pressure points), recent historical incidents. For other vendors: HTML-scrape fallback. Free, no key. Use cases: map an org's internal service topology, detect ongoing outages BEFORE making investigations that require service availability, infer operational maturity from update cadence.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "status_page_finder", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "status_page_finder failed");
    return res.output;
  },
});
