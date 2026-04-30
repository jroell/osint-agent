import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["drug_recalls", "drug_label", "device_events", "food_recalls"])
    .default("drug_recalls")
    .describe(
      "drug_recalls: drug enforcement actions (recalls). drug_label: drug label lookup with boxed warnings + indications + adverse reactions. device_events: MAUDE database (device adverse events, malfunctions, injuries, deaths). food_recalls: food enforcement actions."
    ),
  recalling_firm: z.string().optional().describe("Recalling firm/manufacturer name."),
  product: z.string().optional().describe("Product description fuzzy match."),
  classification: z
    .enum(["Class I", "Class II", "Class III"])
    .optional()
    .describe("Recall severity. Class I = highest (reasonable probability of death/serious adverse health). Class II = temporary or medically reversible. Class III = unlikely to cause adverse health consequences."),
  status: z.string().optional().describe("Recall status: 'Ongoing' | 'Completed' | 'Terminated' | 'Pending'."),
  state: z.string().optional().describe("US state 2-letter code."),
  brand_name: z.string().optional().describe("Drug brand name (e.g. 'OZEMPIC', 'LIPITOR')."),
  generic_name: z.string().optional().describe("Drug generic name (e.g. 'semaglutide', 'atorvastatin')."),
  manufacturer: z.string().optional().describe("Drug or device manufacturer name."),
  search_term: z.string().optional().describe("Free-text search across drug label fields."),
  event_type: z
    .enum(["Death", "Injury", "Malfunction", "Other", "No answer provided"])
    .optional()
    .describe("Device event type."),
  product_problem: z.string().optional().describe("Product problem code or description."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on report_date / date_received."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results (default 10)."),
});

toolRegistry.register({
  name: "openfda_search",
  description:
    "**openFDA — FDA pharma + medical-device + food regulatory data, free no-auth.** Closes the medical-OSINT chain alongside `npi_registry_lookup` (US healthcare providers) and `clinicaltrials_search` (active trials). Four modes: (1) **drug_recalls** — drug enforcement actions filterable by classification (Class I = most severe; reasonable probability of death/serious health consequences), recalling firm, status, date, state. Each entry has product description, reason for recall, distribution pattern. Tested with Class I since 2025-01 → 58 recalls, latest 2026-01-16 McKesson Udenycа temperature abuse + 2025-12-12 ClearLife Allergy Spray microbial contamination; (2) **drug_label** — by brand/generic/manufacturer → indications + **BOXED WARNINGS** (FDA's highest-severity label section, e.g. Ozempic's 'RISK OF THYROID C-CELL TUMORS') + warnings + adverse reactions + contraindications + dosage; (3) **device_events** — MAUDE database, every reported death/injury/malfunction involving a medical device. Filterable by brand/manufacturer/event_type/product_problem/date. Includes patient_problems codes + free-text manufacturer narratives; (4) **food_recalls** — food enforcement actions, same shape as drug_recalls. Pairs with `cfpb_complaints_search` (financial), `federal_register_search` (FDA RFIs/rules), `sec_edgar_search` (publicly-traded pharma 10-Ks).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "openfda_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openfda_search failed");
    return res.output;
  },
});
