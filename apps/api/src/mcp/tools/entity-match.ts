import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["name_match", "name_variations", "username_variations"])
    .optional()
    .describe(
      "name_match: compare two names → similarity scores + verdict (same/likely-same/possible-match/different). name_variations: name → nickname/phonetic/initial variations. username_variations: full name → cross-platform username candidates. Auto-detects: name_a present → name_match, full_name → username_variations, else → name_variations."
    ),
  name_a: z.string().optional().describe("First name to compare (name_match mode)."),
  name_b: z.string().optional().describe("Second name to compare (name_match mode)."),
  name: z.string().optional().describe("Name to expand (name_variations mode). Can include surname; only the given name is expanded."),
  full_name: z
    .string()
    .optional()
    .describe("Full name (username_variations mode), e.g. 'John Q. Doe Jr.'. Honorifics + suffixes auto-stripped."),
});

toolRegistry.register({
  name: "entity_match",
  description:
    "**Pure-compute ER helper — no external APIs, instant, no rate limits, multiplies recall on every name-based search across the catalog.** Three modes: (1) **name_match** — compare two names, returns Levenshtein distance + similarity, Jaro-Winkler similarity, Soundex equality check, and a composite 0–1 score with verdict (same / likely-same / possible-match / different). Critical for dedupe ('do these two records refer to the same person?'). (2) **name_variations** — given a name, returns common nickname/formal variations from an embedded ~150-entry English given-name dictionary (e.g. 'Catherine' → ['Cathy','Kate','Katie','Cat','Trina','Katherine','Kathryn']) plus phonetic Soundex equivalents (catches transliterations like 'Mohammed' / 'Muhammad' / 'Mohamed') plus initial forms ('J. Doe', 'J Doe'). (3) **username_variations** — given a full name, generates cross-platform username candidates (jdoe, j.doe, john.doe, johnd, j_doe, jdoe123, etc.) for use with maigret/sherlock/holehe/site_snippet_search/github_advanced_search. Pure Go, ~5ms latency. Use as a primitive: expand variations first, then search across data sources.",
  inputSchema: input,
  costMillicredits: 0,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "entity_match",
      input: i,
      timeoutMs: 5_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "entity_match failed");
    return res.output;
  },
});
