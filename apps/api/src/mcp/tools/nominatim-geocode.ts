import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["geocode", "reverse"]).default("geocode"),
  query: z.string().optional().describe("Address or place name (geocode mode)"),
  lat: z.number().optional().describe("Latitude for reverse mode (-90 to 90)"),
  lon: z.number().optional().describe("Longitude for reverse mode (-180 to 180)"),
  limit: z.number().int().min(1).max(50).default(5),
  include_address: z.boolean().default(true).describe("Include structured address components in results"),
}).refine(d => (d.mode === "reverse" && d.lat !== undefined && d.lon !== undefined) || (d.mode !== "reverse" && d.query), {
  message: "geocode requires query; reverse requires lat+lon",
});

toolRegistry.register({
  name: "nominatim_geocode",
  description:
    "**Geographic ER via OpenStreetMap Nominatim** — free, no key. Modes: 'geocode' (address/place name → lat/lng + structured address) or 'reverse' (lat/lng → nearest address). Returns: coordinates, OSM IDs, structured address (house/road/city/state/country), bounding box. STRICT 1 req/sec rate limit on free tier. Use cases: connect address records (from `mobile_app_lookup`/`gleif_lei_lookup`/`whois`) to coordinates; reverse-geocode EXIF coordinates from `exif_extract_geolocate`; proximity analysis between entities.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "nominatim_geocode", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "nominatim_geocode failed");
    return res.output;
  },
});
