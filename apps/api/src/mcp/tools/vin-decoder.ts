import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["decode_vin", "recalls", "models_for_make_year"])
    .optional()
    .describe(
      "decode_vin: VIN → vehicle specs. recalls: Make/Model/Year → NHTSA recall campaigns. models_for_make_year: Make+Year → all models. Auto-detects: vin → decode_vin, model → recalls, make → models_for_make_year."
    ),
  vin: z.string().optional().describe("17-character Vehicle Identification Number."),
  make: z.string().optional().describe("Vehicle make (e.g. 'Tesla', 'Ford', 'Honda')."),
  model: z.string().optional().describe("Vehicle model (e.g. 'Model S', 'F-150', 'Civic')."),
  model_year: z.union([z.string(), z.number()]).optional().describe("4-digit model year (e.g. 2017, 2024)."),
});

toolRegistry.register({
  name: "vin_decoder",
  description:
    "**NHTSA Vehicle OSINT — VIN decode + recall lookup + model browser. Free, no auth.** Vehicle OSINT primitive: VINs appear in social media photos, accident reports, used-car listings, insurance fraud cases, parking citations. Three modes: (1) **decode_vin** — 17-char VIN → make / model / model year / manufacturer / vehicle type / body class / engine specs (displacement, cylinders, HP, fuel) / transmission / drive type / plant city/state/country / GVWR / safety equipment standardization (ABS / ESC / BlindSpotMon / AdaptiveCruise / LaneDeparture / ForwardCollision / TPMS / ParkAssist). Tested with Tesla VIN 5YJSA1E25HF205898 → 2017 Tesla Model S, manufactured Fremont CA, passenger car, hatchback. (2) **recalls** — Make/Model/Year → all NHTSA recall campaigns with **consequence + remedy + dates + park-it / over-the-air-update flags**. Tested 2017 Tesla Model S → 9 recalls including 22V037000 FSD 'rolling stop' OTA-fix. (3) **models_for_make_year** — browse all models a manufacturer produced in a year (useful for fuzzy ER: 'Ford 2024 truck' → Maverick / Ranger / F-150 / F-250).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "vin_decoder",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "vin_decoder failed");
    return res.output;
  },
});
