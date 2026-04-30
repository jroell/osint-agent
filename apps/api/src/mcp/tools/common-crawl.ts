import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().min(3).describe("URL or pattern (e.g. '*.example.com')"),
  limit: z.number().int().min(1).max(500).default(50),
  index: z.string().optional().describe("Common Crawl index (default: latest, e.g. 'CC-MAIN-2026-13')"),
});

toolRegistry.register({
  name: "common_crawl_lookup",
  description:
    "Query Common Crawl's CDX index for archived URLs matching a pattern. Free, no key. CC indexes the public web at WARC granularity — useful for 'what existed at this URL pattern' queries that complement Wayback Machine data.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "common_crawl_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "common_crawl_lookup failed");
    return res.output;
  },
});
