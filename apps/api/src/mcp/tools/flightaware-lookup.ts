import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["flight", "operator", "airport", "registration", "track"]).optional(),
  ident: z.string().optional().describe("Callsign or flight number (e.g. 'UAL123', 'BA117')."),
  registration: z.string().optional().describe("Tail number (e.g. 'N12345')."),
  operator: z.string().optional().describe("Airline ICAO code (e.g. 'UAL', 'DAL')."),
  airport: z.string().optional().describe("Airport ICAO code (e.g. 'KSFO', 'EGLL')."),
  fa_flight_id: z.string().optional().describe("AeroAPI flight id for track mode."),
});

toolRegistry.register({
  name: "flightaware_lookup",
  description:
    "**FlightAware AeroAPI v4 — dominant commercial flight tracking. REQUIRES FLIGHTAWARE_API_KEY.** Replaces the now-blocked free adsb.lol mirror. 5 modes: flight (by ident), operator, airport (arrivals+departures), registration (tail#), track (granular position history). Each output emits typed entity envelope (kind: flight) with stable AeroAPI fa_flight_id.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "flightaware_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "flightaware_lookup failed");
    return res.output;
  },
});
