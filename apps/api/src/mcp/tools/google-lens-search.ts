import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  image_url: z.string().url().describe("Public image URL to reverse-search via Google Lens"),
});

toolRegistry.register({
  name: "google_lens_search",
  description:
    "**Google Lens visual search** — runs an image URL through Google Lens (via SerpAPI primary or Serper.dev fallback) and returns visual matches across the web (similar images with title + source domain + link), product matches (when Lens identifies branded items), and related search suggestions. Distinct from gemini_image_analyze (content reasoning): Lens is the world's largest image-to-web INDEX, optimized for finding the original source of an image, identifying specific branded products, and surfacing every page that re-hosts the same image. Strong for: finding the original source of a leaked screenshot/photo, identifying specific products, finding all pages where an image appears. Pairs with gemini_image_analyze for content-reasoning + reverse_image (TinEye/Bing alternative). REQUIRES SERPAPI_KEY (primary) or SERPER_API_KEY (fallback).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "google_lens_search", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_lens_search failed");
    return res.output;
  },
});
