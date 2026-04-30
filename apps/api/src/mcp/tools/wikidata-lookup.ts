import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "entity", "sparql"])
    .optional()
    .describe(
      "search: fuzzy text → top entity candidates (Q-ids + labels + descriptions). entity: full structured card from a QID. sparql: raw SPARQL pass-through. If omitted, auto-detects: 'qid' present → entity, 'sparql' present → sparql, otherwise → search."
    ),
  query: z
    .string()
    .optional()
    .describe("Free-text query (search mode) or any informational hint (used as label)."),
  qid: z
    .string()
    .optional()
    .describe("Wikidata QID, e.g. 'Q7747' for Vladimir Putin. Required for entity mode."),
  sparql: z
    .string()
    .optional()
    .describe(
      "Raw SPARQL query string (sparql mode). Endpoint is https://query.wikidata.org/sparql. 60s timeout. Result is capped at 200 rows. Use the Wikidata prefixes (wd:, wdt:, p:, ps:, pq:, schema:, rdfs:, etc.) — they're already bound."
    ),
  limit: z
    .number()
    .int()
    .min(1)
    .max(50)
    .optional()
    .describe("Result limit for search mode (default 8, max 50)."),
});

toolRegistry.register({
  name: "wikidata_lookup",
  description:
    "**Wikidata structured-knowledge lookup** — fully free, no auth. Three modes: (1) **search** disambiguates a name (e.g. 'Vladimir Putin' → Q7747); (2) **entity** pulls a richly-grouped card from a QID covering 50+ curated OSINT properties (identity: birth/death dates+places, citizenship, gender; family: spouse/parents/children/siblings WITH START AND END DATES; career: positions held, employers, educated-at, doctoral-advisor, awards — also with start/end dates; organization: HQ, founder, CEO, parent/subsidiary, LEI, ticker, ISIN, employee count, revenue; cross-platform identifiers: twitter/github/linkedin/orcid/google-scholar/imdb/etc. — pure ER pivot gold); (3) **sparql** passes a raw SPARQL query through to query.wikidata.org for queries the structured card can't express (e.g. 'all CEOs whose spouse is also a CEO', 'doctoral grandparents', 'subsidiaries of subsidiaries'). **Why this is SOTA**: Wikidata is the only open ~110M-item structured knowledge graph with **temporal qualifiers** (spouse 1983-2014, employer KGB 1975-1990, position Prime Minister 1999-2000) at no cost. The temporal context is what other free OSINT sources lack — this is the substrate for connecting-the-dots reasoning.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wikidata_lookup",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikidata_lookup failed");
    return res.output;
  },
});
