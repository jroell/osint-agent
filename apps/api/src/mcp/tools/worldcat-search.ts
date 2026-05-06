import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "by_oclc", "by_isbn"]).optional(),
  query: z.string().optional(),
  oclc: z.string().optional(),
  isbn: z.string().optional(),
});

toolRegistry.register({
  name: "worldcat_search",
  description:
    "**WorldCat (OCLC) — global library catalog with ~500M records, free public search; optional WORLDCAT_API_KEY (wskey) for higher quotas.** Modes: search (free-text), by_oclc, by_isbn. Each result emits typed entity envelope (kind: book) with stable OCLC numbers + WorldCat URLs. Note: WorldCat search is rate-limited; use HathiTrust or LoC catalog for reliable book lookups.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "worldcat_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "worldcat_search failed");
    return res.output;
  },
});
