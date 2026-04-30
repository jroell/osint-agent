import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["text_search", "place_details", "nearby_search"])
    .default("text_search")
    .describe(
      "text_search: keyword query (e.g. 'Vurvey Cincinnati') → list of places. place_details: full record by place_id (phone/website/reviews/photos). nearby_search: POIs near a lat/lng with optional type filter."
    ),
  query: z
    .string()
    .optional()
    .describe("Free-text query for text_search; or 'keyword' filter for nearby_search."),
  place_id: z
    .string()
    .optional()
    .describe("Google Place ID (required for place_details). Obtained from a prior text_search/nearby_search."),
  lat: z.number().optional().describe("Latitude for nearby_search or location-bias on text_search."),
  lon: z.number().optional().describe("Longitude for nearby_search or location-bias on text_search."),
  radius_m: z
    .number()
    .int()
    .min(1)
    .max(50_000)
    .optional()
    .describe("Search radius in meters (nearby_search default 1000m, max 50km)."),
  type: z
    .string()
    .optional()
    .describe("Place type filter for nearby_search (e.g. 'restaurant', 'lawyer', 'hospital'). See Google Places types reference."),
  keyword: z
    .string()
    .optional()
    .describe("Keyword filter for nearby_search (matches name, type, address, reviews)."),
});

toolRegistry.register({
  name: "google_maps_places",
  description:
    "**Google Maps Places API** — three modes for geographic OSINT: (1) text_search ('Vurvey Cincinnati' → place candidates with address/coords/rating), (2) place_details (place_id → full record: phone, website, opening hours, **reviews** with author+text+rating, **photos** with signed URLs, business status), (3) nearby_search (lat/lng + radius + type → POIs around a point). **Why this is high-leverage for ER**: phone numbers + websites surface directly here (rare in other indices), reviews are a SOURCE-OF-TRUTH for **negative-press / dispute signals** (★1-★2 reviews flagged in highlight_findings as 'potential dispute/complaint signal' — e.g. a customer accusing a business of non-payment is a real OSINT artifact), and place_id is a stable identifier re-queryable forever. Pairs with osm_overpass_query (free OSM data) and nominatim_geocode (free address→coords) for coverage. Requires GOOGLE_MAPS_API_KEY.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "google_maps_places",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_maps_places failed");
    return res.output;
  },
});
