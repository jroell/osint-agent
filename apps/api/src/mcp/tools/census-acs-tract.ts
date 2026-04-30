import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["tract_demographics", "variable_lookup"])
    .optional()
    .describe(
      "tract_demographics: by GEOID → curated demographic profile. variable_lookup: by Census variable code(s) → label + concept. Auto-detects: variable_codes present → variable_lookup, else → tract_demographics."
    ),
  geoid: z
    .string()
    .optional()
    .describe(
      "11-character Census tract GEOID (state[2] + county[3] + tract[6]). Get one from `census_geocoder` for any US address."
    ),
  state_fips: z.string().optional().describe("Alternative to geoid: 2-char state FIPS code."),
  county_fips: z.string().optional().describe("Alternative to geoid: 3-char county FIPS code."),
  tract_code: z.string().optional().describe("Alternative to geoid: 6-char tract code."),
  variable_codes: z
    .union([z.array(z.string()), z.string()])
    .optional()
    .describe("Census variable codes (e.g. ['B19013_001E','B25077_001E']) for variable_lookup mode."),
  year: z.number().int().min(2010).max(2030).optional().describe("ACS5 year (default 2022 — typical lag is 2 years)."),
});

toolRegistry.register({
  name: "census_acs_tract",
  description:
    "**Census ACS5 demographic profile by tract GEOID — free no-auth.** Pairs natively with `census_geocoder`: address → GEOID → demographics. Returns a curated profile with computed percentages: total population, median household income, median home value, median gross rent, unemployment rate, race/ethnicity breakdown (White/Black/Asian alone + Hispanic ethnicity), housing tenure (% owner vs renter), educational attainment (% bachelor's+, breaks out grad+), and long-commute share (90+ min). **Why this is unique ER**: instantly characterizes the neighborhood any US address sits in. Tested with GEOID 24033802405 (Census Tract 8024.05, Prince George's County, MD) → pop 3,957, median income $75,833, 67% Black, 33% homeownership, 19% bachelor's+. Two modes: tract_demographics (the main mode), variable_lookup (resolve Census variable code → label/concept for documentation).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "census_acs_tract",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "census_acs_tract failed");
    return res.output;
  },
});
