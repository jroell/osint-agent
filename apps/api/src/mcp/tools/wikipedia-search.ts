import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["summary", "search", "article_meta"])
    .optional()
    .describe(
      "summary: title → REST summary with extract. search: keyword → matching articles. article_meta: title → categories + recent revisions + length. Auto-detects: title → summary, query → search."
    ),
  title: z.string().optional().describe("Article title (e.g. 'Carl Sagan'). Spaces allowed."),
  query: z.string().optional().describe("Free-text search query (search mode)."),
  lang: z.string().optional().describe("Wikipedia language code (default 'en'). E.g. 'es', 'de', 'ja', 'zh'."),
  limit: z.number().int().min(1).max(50).optional().describe("Max search results (default 10)."),
});

toolRegistry.register({
  name: "wikipedia_search",
  description:
    "**Wikipedia article-level access — REST + MediaWiki APIs, free no-auth.** Distinct from `wikidata_lookup` (structured data, Q-IDs, properties) and `wikipedia_user_intel` (account-level activity) — this surface is article TEXT. Three modes: (1) **summary** — title → REST summary with description + extract (lead paragraph, ~250 words) + thumbnail URL + last-edit timestamp + content URL + wikidata cross-reference. Tested 'Carl Sagan' → American astronomer 1934-1996 with full bio extract. (2) **search** — keyword → matching articles with HTML-marked snippets (`**bolded**` matches), word count, byte size, last-edit timestamp. **Article creation around current events is a unique ER signal** — tested 'Anthropic AI safety' → 159 hits including brand-new 'Anthropic-United States Department of Defense dispute' article edited TODAY (real-time topic emergence detection). (3) **article_meta** — title → categories (taxonomic placement, 20+ entries), recent revisions (who's editing + when), article length, last-rev id. Pairs with `wikidata_lookup` for full Wikipedia↔Wikidata cross-reference.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wikipedia_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikipedia_search failed");
    return res.output;
  },
});
