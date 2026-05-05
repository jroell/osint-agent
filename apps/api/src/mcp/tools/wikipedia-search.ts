import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["summary", "search", "article_meta", "article_content"])
    .optional()
    .describe(
      "summary: title → REST summary with extract (lead only, ~500 chars). search: keyword → matching articles. article_meta: title → categories + recent revisions + length. article_content: title → FULL plain-text article body, pageable via start_offset/max_chars. Auto-detects: title → summary, query → search."
    ),
  title: z.string().optional().describe("Article title (e.g. 'Carl Sagan'). Spaces allowed."),
  query: z.string().optional().describe("Free-text search query (search mode)."),
  lang: z.string().optional().describe("Wikipedia language code (default 'en'). E.g. 'es', 'de', 'ja', 'zh'."),
  limit: z.number().int().min(1).max(50).optional().describe("Max search results (default 10)."),
  start_offset: z.number().int().min(0).optional().describe("article_content mode: char offset to start reading from (default 0). Use returned next_offset to page through long articles."),
  max_chars: z.number().int().min(1000).max(30000).optional().describe("article_content mode: max chars per call (default 12000, max 30000)."),
});

toolRegistry.register({
  name: "wikipedia_search",
  description:
    "**Wikipedia article-level access — REST + MediaWiki APIs, free no-auth.** Distinct from `wikidata_lookup` (structured data, Q-IDs, properties) and `wikipedia_user_intel` (account-level activity) — this surface is article TEXT. Four modes: (1) **summary** — title → REST summary with description + extract (lead paragraph, ~500 chars) + thumbnail URL + last-edit timestamp + content URL + wikidata cross-reference. Tested 'Carl Sagan' → American astronomer 1934-1996 with full bio extract. (2) **search** — keyword → matching articles with HTML-marked snippets (`**bolded**` matches), word count, byte size, last-edit timestamp. **Article creation around current events is a unique ER signal** — tested 'Anthropic AI safety' → 159 hits including brand-new 'Anthropic-United States Department of Defense dispute' article edited TODAY (real-time topic emergence detection). (3) **article_meta** — title → categories (taxonomic placement, 20+ entries), recent revisions (who's editing + when), article length, last-rev id. (4) **article_content** — title → FULL plain-text article body (typically 30k–300k chars). Pageable via start_offset / max_chars (default 12000, max 30000). **Critical for multi-hop fact extraction** where the answer is buried in a body section (Career, Filmography, Discography, Awards) — the lead summary rarely has the specific fact. Tested 'Bill Gates' → 68k chars total. Pairs with `wikidata_lookup` for full Wikipedia↔Wikidata cross-reference.",
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
