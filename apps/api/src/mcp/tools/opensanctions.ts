import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  name: z.string().min(2).describe("Person or company name to screen against sanctions lists, PEP databases, and crime/regulatory enforcement"),
  schema: z.string().default("Thing").describe("FollowTheMoney schema (e.g. 'Person', 'Company', 'Thing' to match anything)"),
  dataset: z.string().default("default").describe("OpenSanctions dataset (e.g. 'default', 'sanctions', 'peps')"),
});

toolRegistry.register({
  name: "opensanctions_screen",
  description:
    "Screen a name against OpenSanctions's watchlist database: international sanctions (OFAC, EU, UK), politically-exposed persons, regulatory enforcement actions, criminal designations. REQUIRES OPENSANCTIONS_API_KEY env var (anonymous access was retired in 2024; free tier at https://www.opensanctions.org/api/).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "opensanctions_screen",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "opensanctions_screen failed");
    return res.output;
  },
});
