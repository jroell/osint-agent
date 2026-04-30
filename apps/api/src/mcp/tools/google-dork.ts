import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Full search query, including any 'site:', 'intitle:', 'inurl:' operators"),
  limit: z.number().int().min(1).max(100).default(30),
  engines: z.array(z.enum(["duckduckgo", "mojeek", "bing"])).optional()
    .describe("Subset of engines to query (default: all 3)"),
});

toolRegistry.register({
  name: "google_dork_search",
  description:
    "Run a search query (typically site-restricted, e.g. `site:linkedin.com/in/ \"<name>\"`) across THREE keyless HTML search engines IN PARALLEL: DuckDuckGo, Mojeek, Bing. Aggregates and deduplicates results across engines. Critical for queries the major search APIs don't cover affordably (LinkedIn lookup, leaked-doc discovery, GitHub email leakage). Engine rotation makes it resilient to single-engine rate limiting.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "google_dork_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_dork_search failed");
    return res.output;
  },
});
