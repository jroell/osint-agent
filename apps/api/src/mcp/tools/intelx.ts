import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Selector — email, domain, IP, hash, BTC address, etc."),
  limit: z.number().int().min(1).max(500).default(50),
});

toolRegistry.register({
  name: "intelx_search",
  description:
    "Search Intelligence X across leaks, paste sites, and dark-web sources. REQUIRES INTELX_API_KEY env var (paid, https://intelx.io/account?tab=developer). Useful for breach selectors and exposed-document discovery.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "intelx_search",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "intelx_search failed");
    return res.output;
  },
});
