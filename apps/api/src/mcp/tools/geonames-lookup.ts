import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "find_nearby", "country_info"]).optional(),
  query: z.string().optional(),
  country: z.string().optional().describe("ISO country code filter for search."),
  feature_class: z.string().optional().describe("GeoNames feature class (A, P, S, T, etc.)."),
  latitude: z.number().optional().describe("For find_nearby."),
  longitude: z.number().optional().describe("For find_nearby."),
  radius_km: z.number().optional().describe("For find_nearby (default 20)."),
  country_code: z.string().optional().describe("ISO country code for country_info."),
});

toolRegistry.register({
  name: "geonames_lookup",
  description:
    "**GeoNames (api.geonames.org) — ~12M place names with rich attributes (admin codes, elevation, population, time zones, neighbours). Requires GEONAMES_USERNAME (free registration); falls back to 'demo' (rate-limited).** Modes: search (place-name with country/feature filters), find_nearby (by lat/lon + radius), country_info. Stronger than Nominatim alone for coordinates ↔ admin context. Each output emits typed entity envelope (kind: place) with stable GeoName ID.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "geonames_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "geonames_lookup failed");
    return res.output;
  },
});
