import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["geocode", "reverse"])
    .optional()
    .describe(
      "geocode: free-text address → standardized address + coords + Census tract. reverse: lat/lon → nearest street + Census geographies. Auto-detects: lat/latitude present → reverse, else → geocode."
    ),
  address: z
    .string()
    .optional()
    .describe("Free-text US address (e.g. '4600 Silver Hill Rd Washington DC' or 'One Apple Park Way Cupertino CA 95014'). Required for geocode mode."),
  latitude: z.number().optional().describe("Latitude for reverse mode."),
  longitude: z.number().optional().describe("Longitude for reverse mode."),
  lat: z.number().optional().describe("Alias for latitude."),
  lon: z.number().optional().describe("Alias for longitude."),
});

toolRegistry.register({
  name: "census_geocoder",
  description:
    "**US Census Geocoder — address normalization + lat/lon + Census tract/county FIPS, free no-auth.** ER primitive that compounds across the catalog: when other tools (`npi_registry_lookup`, `openfda_search` recalls, `cfpb_complaints_search`, `govtrack_search` member offices, `lda_lobbying_search` registrants, `sec_edgar_search` company addresses) return inconsistently-formatted addresses, run them through Census Geocoder for canonical form (uppercase, abbreviated suffix, full ZIP) plus coords. Two modes: (1) **geocode** — free-text address → matched standardized address + lat/lon + parsed components (street name, suffix, city, state, ZIP) + TIGER Line ID (street-segment dedupe key) + Census tract / county / state hierarchy with FIPS GEOIDs (cross-reference keys into Census ACS demographic data); (2) **reverse** — lat/lon → Census geographies. Tested with '4600 Silver Hill Rd Washington DC' → matched to Suitland MD with Census tract 24033802405 in Prince George's County. Pairs with anything that returns US addresses.",
  inputSchema: input,
  costMillicredits: 0,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "census_geocoder",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "census_geocoder failed");
    return res.output;
  },
});
