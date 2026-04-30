import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(1).describe("Wikipedia username (with or without 'User:' prefix)"),
  wiki_lang: z.string().optional().describe("Wikipedia language code: en (default), fr, de, ja, ru, es, etc."),
  wiki_host: z.string().optional().describe("Override host (e.g. 'commons.wikimedia.org', 'en.wiktionary.org', 'www.wikidata.org')"),
  contrib_limit: z.number().int().min(1).max(500).default(100),
});

toolRegistry.register({
  name: "wikipedia_user_intel",
  description:
    "**Wikipedia editor deep dive** — pulls user metadata + recent contributions from any Wikimedia wiki via the public MediaWiki API. Returns: profile (user_id [lower = older], total edit count, registration date, account age in years, user groups/rights, gender, blocked status), recent contributions (title, timestamp, comment, size diff), top edited articles aggregation (interest graph), namespace breakdown (article vs talk vs project space), edit-hour distribution UTC, inferred timezone, oldest/newest contrib timestamps. Highlights flag: very young accounts with high edit counts (sockpuppet pattern), extremely prolific editors (50K+ = paid/SME), notable user groups, blocked users. Cross-wiki via wiki_lang ('fr','de') or wiki_host ('commons.wikimedia.org','www.wikidata.org'). Use cases: COI detection (editing one's own employer's article repeatedly), behavioral ER, timezone inference, sockpuppet flagging, expertise profiling. Free, no auth.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "wikipedia_user_intel", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikipedia_user_intel failed");
    return res.output;
  },
});
