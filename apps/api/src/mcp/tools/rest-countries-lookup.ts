import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["by_name", "by_code", "by_region"])
    .optional()
    .describe(
      "by_name: fuzzy/exact name match. by_code: ISO 3166-1 alpha-2/alpha-3 or IOC. by_region: list countries in a region. Auto-detects: code → by_code, region → by_region, else → by_name."
    ),
  name: z.string().optional().describe("Country name or substring (e.g. 'Germany', 'United States', 'south korea')."),
  query: z.string().optional().describe("Alias for name."),
  full_text: z.boolean().optional().describe("If true, requires exact name match (e.g. 'Korea' alone returns N+S Korea, but with full_text=true requires 'South Korea')."),
  code: z.string().optional().describe("ISO 3166-1 alpha-2 (e.g. 'DE'), alpha-3 (e.g. 'DEU'), or IOC code (e.g. 'GER')."),
  region: z
    .string()
    .optional()
    .describe("Region name (e.g. 'Europe', 'Asia', 'Americas', 'Africa', 'Oceania', 'Antarctic')."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results for by_region (default 30, sorted by population desc)."),
});

toolRegistry.register({
  name: "rest_countries_lookup",
  description:
    "**REST Countries — international country reference data, free no-auth, 250+ countries.** Three modes: (1) **by_name** — fuzzy or full-text exact match. Tested 'Germany' → Berlin capital, EUR currency, German language, 9 borders (AUT/BEL/CZE/DNK/FRA/LUX/NLD/POL/CHE), area 357,114 km², population 83.5M, native name 'Deutschland', Gini 31.9 (2016). (2) **by_code** — ISO 3166-1 alpha-2 (DE), alpha-3 (DEU), or IOC code (GER). Tested 'USA' → 268 calling code suffixes (one per area code). (3) **by_region** — Europe/Asia/Americas/Africa/Oceania/Antarctic → countries sorted by population desc. Returns: native names, capital(s), currencies (code+symbol), languages, neighboring country codes, area km², population, latlng, calling code root + suffixes, timezones, Gini index + year, demonym, FIFA code, car driving side (left/right), continents, Google Maps + OpenStreetMap URLs, flag PNG URL, independence + UN-member status. Pairs with `gleif_lei_lookup` (corp jurisdiction codes), `opensanctions` (country-coded sanctions), `govtrack_search` (legislators by state).",
  inputSchema: input,
  costMillicredits: 0,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "rest_countries_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "rest_countries_lookup failed");
    return res.output;
  },
});
