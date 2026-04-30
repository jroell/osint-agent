import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search_books", "search_authors", "isbn_lookup"])
    .optional()
    .describe(
      "search_books: title/author/subject query → matching books. search_authors: fuzzy author name → bio + work count + alt names. isbn_lookup: ISBN → book metadata. Auto-detects: isbn → isbn_lookup, author → search_authors, else → search_books."
    ),
  title: z.string().optional().describe("Book title filter (search_books mode)."),
  author: z.string().optional().describe("Author name (search_books or search_authors)."),
  subject: z.string().optional().describe("Subject/topic filter (search_books mode)."),
  query: z.string().optional().describe("Free-text query — used as `q` for search_books, or fallback for search_authors."),
  isbn: z.string().optional().describe("10- or 13-digit ISBN (isbn_lookup mode). Hyphens stripped automatically."),
  limit: z.number().int().min(1).max(50).optional().describe("Max results (default 10 books / 5 authors)."),
});

toolRegistry.register({
  name: "openlibrary_search",
  description:
    "**OpenLibrary — book/author ER, free no-auth, ~30M books + 5M authors (operated by Internet Archive).** Three modes: (1) **search_books** — by title/author/subject/query → books with first-publish year + publishers + ISBN list + subject taxonomy + languages + LCC/Dewey classifications + work key; (2) **search_authors** — fuzzy author name → bio + birth/death dates + work count + top work + top subjects + alternate names (e.g. 'Carl Sagan' → OL450295A, 1934–1996, 49 works, top work 'Cosmos', 6 alternate names including 'Dr. Carl Sagan'); (3) **isbn_lookup** — ISBN → book detail with resolved author names. **Why this matters for ER**: author bibliographies cross-reference academic identity (papers via `crossref_paper_search` / `openalex_search`), legal scholarship (via `documentcloud_search`), and Wikipedia entries (via `wikidata_lookup`). Alternate-name lists catch publishing pseudonyms, transliteration variants, and 'Dr.'/'Prof.' honorifics that screw up `entity_match` name comparisons.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "openlibrary_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openlibrary_search failed");
    return res.output;
  },
});
