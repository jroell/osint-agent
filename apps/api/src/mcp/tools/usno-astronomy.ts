import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["day_data", "phases"])
    .optional()
    .describe(
      "day_data: date + lat/lon → sun/moon ephemeris. phases: year → all moon phase milestones. Auto-detects: year present → phases, else → day_data."
    ),
  date: z.string().optional().describe("YYYY-MM-DD (day_data mode, default today UTC)."),
  latitude: z.number().optional().describe("Latitude (decimal degrees)."),
  longitude: z.number().optional().describe("Longitude (decimal degrees)."),
  lat: z.number().optional().describe("Alias for latitude."),
  lon: z.number().optional().describe("Alias for longitude."),
  timezone_offset_hours: z.number().optional().describe("Hours offset from UTC (e.g. -7 for PDT, -4 for EDT, 5.5 for IST). Default 0 (UTC)."),
  year: z.number().int().optional().describe("Year for phases mode (e.g. 2026)."),
});

toolRegistry.register({
  name: "usno_astronomy",
  description:
    "**US Naval Observatory astronomy — sun/moon ephemeris, free no-auth.** Authoritative celestial data. Two modes: (1) **day_data** — date + lat/lon → **sun events** (Begin Civil Twilight, Rise, Upper Transit (solar noon), Set, End Civil Twilight) + **moon events** (Rise, Set, Upper Transit) + current moon phase (Waxing Crescent, First Quarter, Waxing Gibbous, Full, Waning Gibbous, Last Quarter, Waning Crescent, New) + **fraction illuminated** (e.g. '50%') + closest phase milestone with date/time. Tested 2024-04-15 SF → sun rise 06:34 / set 19:46, moon First Quarter (50% illuminated). (2) **phases** — year → all 4 moon-phase dates (~48 milestones per year) for that year. **Forensic OSINT use cases**: photo timestamp/location verification ('sunset photo at 6 PM' → was sun actually setting at lat 60°N in winter? — usually no), moon phase claim verification ('full moon photo Wednesday' → was actually waxing crescent), astronomical-twilight darkness checks. Pairs with `openmeteo_search` (cloud cover at the same time — was the sun/moon visible?) for full sky-state forensic OSINT.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "usno_astronomy",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "usno_astronomy failed");
    return res.output;
  },
});
