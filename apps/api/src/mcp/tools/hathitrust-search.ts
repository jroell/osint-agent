import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "volume_by_id", "volume_by_oclc", "volume_by_isbn"]).optional(),
  query: z.string().optional().describe("Free-text catalog search."),
  hathitrust_id: z.string().optional().describe("HathiTrust volume id (e.g. 'mdp.39015005556579')."),
  oclc: z.string().optional().describe("OCLC number for volume_by_oclc mode."),
  isbn: z.string().optional().describe("ISBN for volume_by_isbn mode."),
});

toolRegistry.register({
  name: "hathitrust_search",
  description:
    "**HathiTrust Digital Library — ~17M digitized books/periodicals from major US/UK research libraries, free no-key.** Modes: search (free-text), volume_by_id, volume_by_oclc, volume_by_isbn. The bibliographic-API modes (oclc/isbn/id) are the reliable path; the catalog search is fronted by Cloudflare and may return 403 — use it as a hint, fall back to OCLC/ISBN lookup. Each result emits typed entity envelope (kind: book) with stable HathiTrust handle URLs and OCLC/ISBN/LCCN cross-references.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hathitrust_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hathitrust_search failed");
    return res.output;
  },
});
