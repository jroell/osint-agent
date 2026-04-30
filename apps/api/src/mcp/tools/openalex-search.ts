import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["works", "authors", "author_works"]).default("works"),
  query: z.string().min(2).describe("Search query — keyword/title/author name (works/authors mode), or OpenAlex author ID 'A5066197394' for author_works"),
  limit: z.number().int().min(1).max(200).default(20),
});

toolRegistry.register({
  name: "openalex_search",
  description:
    "**Academic ER via OpenAlex** — open-replacement for Microsoft Academic Graph. ~250M scholarly works, ~100M authors, ~200K institutions indexed. Free, no key. Modes: 'works' (paper search by title/keyword), 'authors' (researcher profiles with h-index, citations, affiliations), 'author_works' (a specific author's papers — pass their OpenAlex ID like 'A5066197394' from a prior author lookup). Use cases: domain-expert finding (h-index ranks scholarly impact), competitive R&D intel, recruiting (find researchers prolific in a field), academic identity → institutional affiliation chain.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "openalex_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openalex_search failed");
    return res.output;
  },
});
