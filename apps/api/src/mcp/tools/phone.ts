import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  number: z.string().min(5).describe("Phone number (E.164 format preferred, e.g. '+14155552671')"),
});

toolRegistry.register({
  name: "phone_numverify",
  description:
    "Validate a phone number and return its country, carrier, and line type (mobile/landline/VoIP). REQUIRES NUMVERIFY_API_KEY env var (free tier: 250 req/month, https://numverify.com/product).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "phone_numverify",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "phone_numverify failed");
    return res.output;
  },
});
