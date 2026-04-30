import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  prompt: z.string().min(2).describe("Computation task — e.g. 'Compute mean and median of these numbers', 'Extract all phone numbers and count by area code', 'Compute days between these dates'"),
  data_context: z.string().optional().describe("Optional: paste data context (list of values, JSON, text body) that the prompt should operate on"),
  model: z.string().default("gemini-3.1-pro-preview"),
});

toolRegistry.register({
  name: "gemini_code_execution",
  description:
    "**Gemini Python sandbox via code_execution tool** — Gemini iteratively writes Python, executes it, observes output, and writes more Python if needed. Returns: each (code, output) iteration + final text explanation. Pairs with every other tool: fetch data via X → compute summary stats / dedupe / sort / filter / format via this. Strong for: date arithmetic, numerical aggregation (mean/median/percentile), string parsing (extract emails/phones/etc and count by domain/area-code), data wrangling. Investigators can audit the exact code Gemini ran for verification. REQUIRES GOOGLE_AI_API_KEY.",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gemini_code_execution", input: i, timeoutMs: 240_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gemini_code_execution failed");
    return res.output;
  },
});
