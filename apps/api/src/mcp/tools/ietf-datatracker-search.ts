import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["person_search", "person_docs", "doc_search", "doc_lookup"]).default("person_search"),
  query: z.string().min(2).describe("Person name (person_search), person ID OR name (person_docs), title keyword or 'draft-' / 'rfc' prefix (doc_search), exact doc name (doc_lookup, e.g. 'rfc8446')"),
  limit: z.number().int().min(1).max(200).default(25),
});

toolRegistry.register({
  name: "ietf_datatracker_search",
  description:
    "**IETF datatracker ER for protocol/cryptography/network researchers** — queries the public datatracker.ietf.org API (~50 years of internet protocol design, no auth needed). 4 modes: person_search (name → IETF person ID), person_docs (KILLER FEATURE: returns affiliation + email AT TIME OF EACH document — Jim Schaad's 105 docs reveal career trail Microsoft → Soaring Hawk Consulting + email evolution jimsch@microsoft.com → ietf@augustcellars.com), doc_search (title keyword or draft-* prefix), doc_lookup (by exact doc name like 'rfc8446' or 'draft-ietf-tls-rfc8446bis'). Aggregations: unique affiliations across docs, unique emails across docs, working groups represented. Strong novel ER: IETF is one of the highest-trust public sources for cryptographer/network/security identity, with the unique property that affiliations are STAMPED PER-DOCUMENT — giving a temporal employer trail.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ietf_datatracker_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ietf_datatracker_search failed");
    return res.output;
  },
});
