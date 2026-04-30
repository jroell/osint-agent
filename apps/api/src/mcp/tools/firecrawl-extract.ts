import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().describe("URL to scrape and extract from"),
  mode: z.enum(["extract", "schema"]).default("extract"),
  prompt: z.string().min(2).optional().describe("Natural-language description of what to extract (e.g. 'Extract company name, founders, mission, and product list')"),
  schema: z.record(z.string(), z.any()).optional().describe("Optional JSON schema object for strict-typing extraction"),
  include_markdown: z.boolean().default(false).describe("Also return rendered Markdown excerpt"),
  stealth_proxy: z.boolean().default(false).describe("Use Firecrawl's stealth proxy (more credits, defeats some Cloudflare)"),
}).refine((d) => d.prompt || d.schema, { message: "Either prompt or schema is required" });

toolRegistry.register({
  name: "firecrawl_extract",
  description:
    "**LLM-powered structured extraction from any URL** — wraps Firecrawl's `/scrape` with `formats:[json]` + jsonOptions. Pass a natural-language prompt OR a JSON schema, get back structured fields directly. Eliminates per-site HTML parsing for any URL Firecrawl can scrape (works on SPAs, JS-heavy sites, most Cloudflare-protected sites with stealth_proxy=true). Use cases: extract org charts from company about pages, parse arbitrary news articles into structured fields, get specific facts from any URL without writing parsers. Pairs with site_snippet_search (snippet-bypass for indexed-but-blocked sites) — together they form a two-tier scraping strategy: snippet-bypass for blocked content + full-page LLM extraction for everything else. REQUIRES FIRECRAWL_API_KEY.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "firecrawl_extract", input: i, timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "firecrawl_extract failed");
    return res.output;
  },
});
