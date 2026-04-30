import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["current", "historical", "air_quality"])
    .optional()
    .describe(
      "current: live conditions at lat/lon. historical: date range daily aggregates since 1940. air_quality: current pollutant levels + US/EU AQI. Auto-detects: start_date present → historical, else → current."
    ),
  latitude: z.number().describe("Latitude (decimal degrees)."),
  longitude: z.number().describe("Longitude (decimal degrees)."),
  lat: z.number().optional().describe("Alias for latitude."),
  lon: z.number().optional().describe("Alias for longitude."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound (historical mode). Archive goes back to 1940."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound (defaults to start_date for single-day lookup)."),
});

toolRegistry.register({
  name: "openmeteo_search",
  description:
    "**Open-Meteo — historical weather + air quality + forecast, free no-auth.** Independent Swiss-meteorology nonprofit, fits the 'independent academic infra stays open' pattern. Pairs with `usgs_earthquake_search` for full temporal-spatial forensic OSINT, and with `nominatim_geocode` / `census_geocoder` for address→lat/lon lookups. Three modes: (1) **current** — temperature (°F), humidity, precipitation, wind speed/direction, cloud cover, weather code (WMO standard, decoded to human-readable description like 'Light drizzle' or 'Thunderstorm with heavy hail'); (2) **historical** — date range (since 1940) → daily aggregates with max/min temperature, precipitation total, wind max, weather code. **Forensic OSINT use cases**: alibi corroboration ('user said it was raining at X on Y' → verify against authoritative archive), accident analysis (was visibility low?), social-media post verification, insurance fraud flags ('claimed flood damage on date X' → verify precipitation); (3) **air_quality** — current pollutant levels (PM2.5, PM10, NO₂, O₃, SO₂, CO) + US AQI with EPA category (Good/Moderate/Unhealthy/etc) + European AQI. Tested San Francisco historical 2024-04-15 → high 59°F, low 46.8°F, 0.20\" precipitation, light drizzle. Current AQI 50 (Good).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "openmeteo_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openmeteo_search failed");
    return res.output;
  },
});
