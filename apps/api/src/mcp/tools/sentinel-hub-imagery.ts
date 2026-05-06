import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["true_color", "ndvi", "ndwi", "available_dates"]).optional(),
  bbox: z.array(z.number()).length(4).describe("[minLon, minLat, maxLon, maxLat]"),
  date_from: z.string().optional().describe("ISO datetime (default: 2024-01-01)."),
  date_to: z.string().optional().describe("ISO datetime (default: now)."),
});

toolRegistry.register({
  name: "sentinel_hub_imagery",
  description:
    "**Sentinel Hub Process API — Copernicus satellite imagery, free OAuth tier. REQUIRES SENTINEL_HUB_CLIENT_ID + SENTINEL_HUB_CLIENT_SECRET.** Modes: true_color (RGB), ndvi (vegetation index), ndwi (water index), available_dates (catalog). Returns 512x512 PNG image as data URL plus typed entity envelope (kind: satellite_image). Use for OSINT geolocation verification, before/after comparison, vegetation/water/activity inference.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "sentinel_hub_imagery",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "sentinel_hub_imagery failed");
    return res.output;
  },
});
