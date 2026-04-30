import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  prompt: z.string().min(2).describe("Question or task — e.g. 'What is in this image?', 'Identify the species', 'Compare these two screenshots', 'Read the text', 'Where was this taken?'"),
  image_url: z.string().url().optional().describe("Single image URL"),
  image_urls: z.array(z.string().url()).optional().describe("Up to 8 image URLs (for comparison or multi-image reasoning)"),
  model: z.string().default("gemini-3.1-pro-preview"),
}).refine((d) => d.image_url || (d.image_urls && d.image_urls.length > 0), { message: "image_url or image_urls is required" });

toolRegistry.register({
  name: "gemini_image_analyze",
  description:
    "**Visual OSINT via Gemini multimodal** — pass 1-8 image URLs + a prompt; the tool fetches each image, base64-encodes, and sends to Gemini for visual reasoning. Distinct from geo_vision (geo-only) / reverse_image (similarity) / exif (metadata): Gemini reasons about image *content*. Use cases: landmark/location identification, document OCR + structured extraction from scanned PDFs/screenshots, compare logos/screenshots for manipulation, species/object/vehicle identification, read partial/blurred/handwritten text in leaked images, multi-image comparison ('do these show the same person/place/event?'). Multi-image input enables side-by-side reasoning. REQUIRES GOOGLE_AI_API_KEY.",
  inputSchema: input,
  costMillicredits: 12,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gemini_image_analyze", input: i, timeoutMs: 240_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gemini_image_analyze failed");
    return res.output;
  },
});
