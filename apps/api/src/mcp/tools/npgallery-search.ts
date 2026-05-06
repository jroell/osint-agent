import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "asset_by_id"]).optional(),
  query: z.string().optional(),
  asset_id: z.string().optional().describe("NPGallery asset id."),
});

toolRegistry.register({
  name: "npgallery_search",
  description:
    "**NPS NPGallery — US National Park Service heritage assets (National Register of Historic Places + landmarks + photos), free no-key.** Modes: search (full-text) and asset_by_id. Each result emits typed entity envelope (kind: historic_place | historic_landmark) with stable NPGallery URL. Use for landmark identification chains: erected dates, relocation history, NRHP listings.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "npgallery_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "npgallery_search failed");
    return res.output;
  },
});
