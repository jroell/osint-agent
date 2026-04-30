import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  name: z.string().min(2).describe("Person's full name (e.g. 'Jason Roell')"),
  city: z.string().optional().describe("City filter (e.g. 'Cincinnati')"),
  state: z.string().optional().describe("State or 2-letter code (e.g. 'OH' or 'Ohio')"),
  limit: z.number().int().min(1).max(30).default(10),
});

toolRegistry.register({
  name: "truepeoplesearch_lookup",
  description:
    "**Family-tree OSINT via truepeoplesearch.com** — bypasses TPS's aggressive Cloudflare-protection by harvesting search-engine snippets (Tavily / firecrawl) of pages Google has indexed. The live TPS site requires real-browser JS execution + session cookies that even firecrawl-stealth fails to defeat, but the snippets contain the same structured data. Returns: person records with name, age, birth month/year, current address, lived-since date, **relatives/associates list** (the killer feature — TPS aggregates from voter rolls, property records, marriage records, court filings to surface family relationships), phones, emails. Aggregates unique relatives across all returned hits — critical for queries like 'find a person's father-in-law' which requires enumerating spouse → spouse's parents. REQUIRES TAVILY_API_KEY (or FIRECRAWL_API_KEY fallback). Pairs with findagrave_search (deceased relatives) and fec_donations_lookup (employer-disclosed) for full family-tree ER.",
  inputSchema: input,
  costMillicredits: 6,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "truepeoplesearch_lookup", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "truepeoplesearch_lookup failed");
    return res.output;
  },
});
