import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "biography"]).optional().describe("search: keyword (HTML-only endpoint, may be JS-rendered). biography: fetch one bio by slug."),
  query: z.string().optional().describe("Search keyword."),
  slug: z.string().optional().describe("ADB slug (e.g. 'lawson-henry-7118')."),
});

toolRegistry.register({
  name: "adb_search",
  description:
    "**Australian Dictionary of Biography (adb.anu.edu.au) — authoritative biographical reference for ~13,000 Australians, free scrape-only.** Modes: search (HTML-only, may be JS-rendered) and biography (fetch by slug, reliable). Each output emits typed entity envelope (kind: person) with stable ADB URL. Pairs with `wikitree_lookup` (genealogy) and `trove_search` (newspaper context).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "adb_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "adb_search failed");
    return res.output;
  },
});
