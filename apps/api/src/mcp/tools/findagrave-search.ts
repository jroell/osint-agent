import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search_by_name", "memorial_detail"]).default("search_by_name"),
  first_name: z.string().optional(),
  last_name: z.string().optional(),
  location: z.string().optional().describe("e.g. 'Ohio, USA' or 'Cincinnati, Ohio, USA'"),
  url: z.string().optional().describe("Memorial detail URL (for memorial_detail mode)"),
}).refine(d => (d.mode === "memorial_detail" && d.url) || (d.mode !== "memorial_detail" && d.last_name), {
  message: "search_by_name requires last_name; memorial_detail requires url",
});

toolRegistry.register({
  name: "findagrave_search",
  description:
    "**Genealogical OSINT** — searches FindAGrave (~210M memorial records) via stealth-HTTP (rnet/JA4+ TLS impersonation, bypasses Cloudflare). Modes: 'search_by_name' lists matching memorials with optional location filter; 'memorial_detail' extracts full record with parent/spouse/sibling/child relationships. The killer feature is FAG memorials often list relatives explicitly ('Spouses: John Doe', 'Parents: Jane Smith') — critical for family-tree OSINT (find a deceased relative → discover their living family). Public, free.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "findagrave_search", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "findagrave_search failed");
    return res.output;
  },
});
