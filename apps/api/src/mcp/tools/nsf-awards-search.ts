import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["pi_name", "keyword", "org_name", "award_id"]).default("pi_name"),
  query: z.string().min(2).describe("PI name (pi_name), full-text term (keyword), institution (org_name), or NSF award ID"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "nsf_awards_search",
  description:
    "**NSF (National Science Foundation) awards ER** — queries the public research.gov awards API for non-biomedical US federal research grants (engineering, math, physics, CS, social sciences, geography, education, oceanography). Free, no auth. Direct complement to NIH RePORTER (biomed) — together they cover most US federal academic funding. Modes: pi_name (career trace for a researcher), keyword (full-text title + abstract), org_name (grants at an institution), award_id (direct lookup). Returns: awards with title, abstract, PI name + email + co-PIs, awardee institution + city + state + country, funds obligated, start/end dates, NSF program. Aggregations: top PIs, top institutions with year range (career mobility), unique states, unique funding programs. Surfaces 40-year temporal trails: e.g. Geoffrey Hinton's NSF history shows him at CMU in 1986 ('Search Methods for Massively Parallel Networks') — pre-Google, pre-deep-learning revival.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "nsf_awards_search", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "nsf_awards_search failed");
    return res.output;
  },
});
