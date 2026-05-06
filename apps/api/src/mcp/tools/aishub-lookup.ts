import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["by_mmsi", "by_imo", "by_callsign", "near_position"]).optional(),
  mmsi: z.union([z.string(), z.number()]).optional().describe("9-digit Maritime Mobile Service Identity."),
  imo: z.union([z.string(), z.number()]).optional().describe("IMO number."),
  callsign: z.string().optional(),
  latitude: z.number().optional(),
  longitude: z.number().optional(),
  radius_nm: z.number().optional(),
});

toolRegistry.register({
  name: "aishub_lookup",
  description:
    "**AISHub vessel-AIS feed (data.aishub.net) — REQUIRES AISHUB_USERNAME (free registration).** 4 modes: by_mmsi, by_imo, by_callsign, near_position (bounding box around lat/lon ± radius_nm). Each output emits typed entity envelope (kind: vessel) with MMSI as primary identifier (IMO when present). Returns position, speed, heading, destination, ETA.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "aishub_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "aishub_lookup failed");
    return res.output;
  },
});
