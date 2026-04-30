import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  image_url: z.string().url().describe("Publicly-reachable image URL to reverse-search"),
  limit: z.number().int().min(1).max(100).default(30),
});

toolRegistry.register({
  name: "reverse_image_search",
  description:
    "Reverse-search an image across TinEye and Bing Visual Search and return aggregated matches (page URLs, domains, dimensions). REQUIRES at least one of TINEYE_API_KEY (https://services.tineye.com/) or BING_VISUAL_SEARCH_KEY (Azure). Both are paid. Note: Yandex's official API was retired in 2023 — for Yandex coverage, route via stealth_http_fetch against yandex.com/images/search.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "reverse_image_search",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reverse_image_search failed");
    return res.output;
  },
});
