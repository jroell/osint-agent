import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["author_works", "work_full", "coauthor_graph"])
    .optional()
    .describe("author_works: author record + top 50 works + co-authors. work_full: full work record. coauthor_graph: 1-hop co-author graph."),
  author_id: z.string().optional().describe("OpenAlex author id (A...) or full URL."),
  orcid: z.string().optional().describe("ORCID 0000-0000-0000-0000."),
  work_id: z.string().optional().describe("OpenAlex work id (W...)."),
  doi: z.string().optional().describe("DOI for work_full mode."),
});

toolRegistry.register({
  name: "openalex_author_graph",
  description:
    "**OpenAlex author/work graph traversal — free, polite mailto = higher rate limit.** Built specifically for multi-hop academic chain questions: 'second author on a 2020-2023 paper where the third author lived in Brunswick' is answerable in one or two calls instead of many. Modes: author_works (author + top works + co-authors), work_full (full work record), coauthor_graph (1-hop). Each output emits typed entity envelope (kind: scholar | scholarly_work) with stable OpenAlex IDs (incorporates ORCID/DOI cross-reference). Set `OPENALEX_MAILTO` env var for polite-pool rate limits.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "openalex_author_graph",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openalex_author_graph failed");
    return res.output;
  },
});
