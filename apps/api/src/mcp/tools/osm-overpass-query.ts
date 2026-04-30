import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["name_search", "tag_radius", "tag_in_bbox", "free_form"]).default("name_search"),
  name: z.string().optional().describe("Required for name_search — exact 'name' tag, e.g. 'Trump Tower'"),
  tag_key: z.string().optional().describe("Required for tag_radius/tag_in_bbox — OSM tag key (amenity, shop, military, etc.)"),
  tag_value: z.string().optional().describe("Required for tag_radius/tag_in_bbox — OSM tag value (school, supermarket, base, etc.)"),
  lat: z.number().optional().describe("Center latitude for tag_radius mode"),
  lon: z.number().optional().describe("Center longitude for tag_radius mode"),
  radius_m: z.number().min(1).max(50000).optional().describe("Radius in meters for tag_radius mode (default 500, max 50000)"),
  min_lat: z.number().optional(),
  min_lon: z.number().optional(),
  max_lat: z.number().optional(),
  max_lon: z.number().optional(),
  overpass_ql: z.string().optional().describe("Raw Overpass QL query for free_form mode"),
  limit: z.number().int().min(1).max(200).default(50),
  timeout_seconds: z.number().int().min(5).max(90).default(25),
});

toolRegistry.register({
  name: "osm_overpass_query",
  description:
    "**OpenStreetMap geographic feature query (Overpass API)** — free, no auth. 4 modes: 'name_search' (find every feature tagged 'name=X' worldwide — brand-trace), 'tag_radius' (find all features matching tag within radius of lat/lon — pairs with nominatim_geocode for end-to-end geo ER like 'schools within 500m of address'), 'tag_in_bbox' (same but bounded box), 'free_form' (raw Overpass QL). Returns OSM features with type, OSM ID, lat/lon, full tags map, hoisted address fields, brand, operator, website, phone — plus aggregations (top amenities, brands, countries) and bounding box. Use cases: surveillance OSINT (camera/military tags), brand-chain mapping (every Starbucks worldwide), geographic ER ('what's near this address?'), infrastructure recon (rail, ports, airports, ATMs). OSM contributors tag globally — coverage varies by region.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "osm_overpass_query", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "osm_overpass_query failed");
    return res.output;
  },
});
