import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const scrapeInput = z.object({
  url: z.string().url(),
  include_html: z.boolean().default(false),
  only_main_content: z.boolean().default(true),
  mobile: z.boolean().default(false),
});

toolRegistry.register({
  name: "firecrawl_scrape",
  description:
    "Scrape a URL with full JS rendering and return clean Markdown. Handles SPAs that stealth_http_fetch cannot. Default returns main content only — much more LLM-friendly than raw HTML. REQUIRES FIRECRAWL_API_KEY env var (https://firecrawl.dev).",
  inputSchema: scrapeInput,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "firecrawl_scrape",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "firecrawl_scrape failed");
    return res.output;
  },
});

const searchInput = z.object({
  query: z.string().min(2),
  limit: z.number().int().min(1).max(20).default(10),
  scrape_results: z.boolean().default(false).describe("If true, scrapes each result and returns Markdown content"),
});

toolRegistry.register({
  name: "firecrawl_search",
  description:
    "Search the web AND optionally scrape each result in one call (Firecrawl /search). Set scrape_results=true to get Markdown of every result page. Best when you need both 'find URLs' AND 'read them' in one tool call. REQUIRES FIRECRAWL_API_KEY.",
  inputSchema: searchInput,
  costMillicredits: 12,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "firecrawl_search",
      input: i,
      timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "firecrawl_search failed");
    return res.output;
  },
});
