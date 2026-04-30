import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["org_mentions", "theme_search", "cooccurrence", "tone_trend"]).default("org_mentions"),
  query: z.string().min(2).describe("Entity name (org/person/keyword) for org_mentions/cooccurrence/tone_trend; theme code (e.g. 'CYBER_ATTACK') for theme_search"),
  days_back: z.number().int().min(1).max(90).default(7),
  limit: z.number().int().min(1).max(200).default(25),
});

toolRegistry.register({
  name: "bigquery_gdelt",
  description:
    "**GDELT Global Knowledge Graph via BigQuery** — ~1.7TB of global news intel. Every English news article scraped worldwide every 15min, with extracted entities + sentiment + themes + locations. Modes: 'org_mentions' (find articles mentioning target with sentiment summary + co-mentioned entities + source-domain breakdown), 'theme_search' (filter by GDELT theme code: CYBER_ATTACK, ELECTION_FRAUD, FINANCE_BUDGET, KILL, ARREST, etc — see gdeltproject.org/data/lookups), 'cooccurrence' (top entities co-mentioned with target — reveals network structure), 'tone_trend' (daily sentiment-trend over date range). Date-partitioned for cost control. Most threat-intel platforms charge $$$$ for derivatives of this same dataset.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_gdelt", input: i, timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_gdelt failed");
    return res.output;
  },
});
