import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["by_icao24", "by_registration", "by_callsign", "near_position"]).optional(),
  icao24: z.string().optional().describe("24-bit ICAO hex code (e.g. 'a0001b')."),
  registration: z.string().optional().describe("Tail number (e.g. 'N12345')."),
  callsign: z.string().optional().describe("Flight callsign (e.g. 'UAL123')."),
  latitude: z.number().optional(),
  longitude: z.number().optional(),
  radius_nm: z.number().optional().describe("Search radius in nautical miles (default 25)."),
});

toolRegistry.register({
  name: "adsb_lookup",
  description:
    "**ADS-B aircraft tracking — primary source: free public adsb.lol mirror; ADS-B Exchange RapidAPI tier optional via RAPID_API_KEY.** 4 modes: by_icao24 (24-bit hex), by_registration (tail number), by_callsign (flight ID), near_position (lat/lon + radius_nm). Each output emits typed entity envelope (kind: aircraft) with stable ICAO24 hex IDs.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "adsb_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "adsb_lookup failed");
    return res.output;
  },
});
