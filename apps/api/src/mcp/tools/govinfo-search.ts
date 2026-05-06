import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "package", "collections_list"]).optional(),
  query: z.string().optional(),
  package_id: z.string().optional(),
});

toolRegistry.register({
  name: "govinfo_search",
  description:
    "**GovInfo (api.govinfo.gov) — US Federal Register / Congressional Record / Public Laws / U.S. Code / GAO Reports full-text. REQUIRES GOVINFO_API_KEY (or DATA_GOV_API_KEY) — free at api.data.gov.** Modes: search (full-text across collections), package (summary by package id), collections_list. Each output emits typed entity envelope (kind: publication) with stable GovInfo URLs.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "govinfo_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "govinfo_search failed");
    return res.output;
  },
});
