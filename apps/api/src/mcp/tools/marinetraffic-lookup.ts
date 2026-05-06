import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["vessel_position", "vessel_master_data", "vessels_in_area", "voyage_forecast", "port_calls"]).optional(),
  mmsi: z.union([z.string(), z.number()]).optional(),
  imo: z.union([z.string(), z.number()]).optional(),
  port_calls: z.boolean().optional(),
  lat_min: z.number().optional(),
  lat_max: z.number().optional(),
  lon_min: z.number().optional(),
  lon_max: z.number().optional(),
});

toolRegistry.register({
  name: "marinetraffic_lookup",
  description:
    "**MarineTraffic Service API — paid commercial vessel tracking; REQUIRES MARINETRAFFIC_API_KEY.** 5 modes: vessel_position (current AIS), vessel_master_data (static metadata), vessels_in_area (bbox), voyage_forecast (ETA), port_calls (history). Each output emits typed entity envelope (kind: vessel | port_call) with stable MMSI/IMO. Pairs with the free `aishub_lookup` (community feed) for fallback coverage.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "marinetraffic_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "marinetraffic_lookup failed");
    return res.output;
  },
});
