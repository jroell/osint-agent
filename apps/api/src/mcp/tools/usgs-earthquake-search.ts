import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["recent", "query"])
    .optional()
    .describe(
      "recent: preset feeds (e.g. '2.5_week'). query: custom date/magnitude/bbox/center+radius/depth filters. Auto-detects: start_date or latitude → query, else → recent."
    ),
  feed: z
    .string()
    .optional()
    .describe(
      "Recent-mode feed code. Format: '{mag}_{period}' where mag = significant | 4.5 | 2.5 | 1.0 | all and period = day | week | month | hour. Examples: '2.5_week' (felt-strength last 7d), 'significant_month' (damage-causing last 30d), 'all_hour' (every detected quake last hour). Default: '2.5_week'."
    ),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on event time (query mode)."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound (query mode)."),
  min_magnitude: z.number().optional().describe("Minimum magnitude (query mode, e.g. 5.0 for noticeable, 7.0 for major)."),
  max_magnitude: z.number().optional().describe("Maximum magnitude (query mode)."),
  latitude: z.number().optional().describe("Center-search latitude (query mode, paired with longitude + radius_km)."),
  longitude: z.number().optional().describe("Center-search longitude."),
  radius_km: z.number().optional().describe("Radius from center in km (default 100)."),
  min_latitude: z.number().optional().describe("Bounding-box south edge (alternative to center+radius)."),
  max_latitude: z.number().optional().describe("Bounding-box north edge."),
  min_longitude: z.number().optional().describe("Bounding-box west edge."),
  max_longitude: z.number().optional().describe("Bounding-box east edge."),
  min_depth_km: z.number().optional().describe("Minimum depth (positive km below surface)."),
  max_depth_km: z.number().optional().describe("Maximum depth."),
  alert_level: z.enum(["green", "yellow", "orange", "red"]).optional().describe("USGS PAGER alert filter (yellow+ = damage expected)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max events (default 50, max 200)."),
});

toolRegistry.register({
  name: "usgs_earthquake_search",
  description:
    "**USGS Earthquake Catalog — temporal-spatial forensic OSINT, free no-auth.** Real-time + historical seismic events globally, updated every 5 min. **Why this is unique ER**: alibi corroboration ('user claimed felt-quake at X on Y' → verify against authoritative seismic data), social-media verification (tweets about quake → confirm timing), insurance fraud flags (claim for earthquake damage with no real event nearby/then), time-event correlation (building damage reports aligned to seismic timing). Two modes: (1) **recent** — preset feeds with magnitude+period combos like '2.5_week' (felt-strength past 7d), 'significant_month' (damage-causing past 30d), 'all_hour' (every detected quake past hour); (2) **query** — full FDSN search by date range + magnitude min/max + bbox OR center+radius_km + depth + PAGER alert level (yellow+/orange/red = damage expected). Each event surfaces magnitude (Richter / Mw / Mb / Md), location, depth (km), tsunami flag, PAGER alert color, significance score (0-1000+), origin time (ISO8601 UTC + Unix ms), USGS event URL. Aggregations: tsunami count, alerted count, unique regions. Pairs with `nominatim_geocode` + `census_geocoder` for address-to-coordinate lookups before bbox queries.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "usgs_earthquake_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "usgs_earthquake_search failed");
    return res.output;
  },
});
