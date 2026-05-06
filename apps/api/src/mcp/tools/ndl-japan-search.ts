import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search"]).optional(),
  query: z.string().describe("Search keyword (Japanese or English)."),
});

toolRegistry.register({
  name: "ndl_japan_search",
  description:
    "**National Diet Library (Japan) Digital Collections — ~7.4M items, free no-key OpenSearch API.** Japanese national library covering Meiji-era books, woodblock prints, postwar periodicals, manuscripts. Critical for Japan-specific historical chains where the source-of-record is in Japanese. Each result emits typed entity envelope with type-aware kind (book | image | audio | newspaper | library_item) and stable NDL handle URLs.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "ndl_japan_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ndl_japan_search failed");
    return res.output;
  },
});
