import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["assignee_search", "inventor_search", "keyword_search"]).default("assignee_search"),
  query: z.string().min(2).describe("Company name (assignee), inventor full name, or keyword in title"),
  years_back: z.number().int().min(1).max(30).default(5).describe("Filter by filing date — default last 5 years"),
  limit: z.number().int().min(1).max(200).default(30),
});

toolRegistry.register({
  name: "bigquery_patents",
  description:
    "**Patent intelligence via BigQuery** — `patents-public-data.patents.publications` (~100M records, US PTO + EPO + WIPO + 100+ national offices). Modes: 'assignee_search' (who's patenting in X area? — fuzzy company name), 'inventor_search' (their portfolio — recruiting goldmine), 'keyword_search' (title contains phrase). Returns patents with titles + dates + assignees + inventors + Google Patents URLs, plus aggregations: top assignees, top inventors, unique inventor list. Use cases: competitive intel, prior-art search, M&A signals, recruiting (find inventors prolific in your domain).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_patents", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_patents failed");
    return res.output;
  },
});
