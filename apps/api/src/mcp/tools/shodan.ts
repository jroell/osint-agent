import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(1).describe("Shodan search query (e.g. 'hostname:example.com', 'port:22 country:US', 'org:\"Anthropic\"')"),
  limit: z.number().int().min(1).max(200).default(50),
});

toolRegistry.register({
  name: "shodan_search",
  description:
    "Query Shodan's internet-wide scan database for hosts/services matching a query. REQUIRES the SHODAN_API_KEY env var (paid, https://account.shodan.io/billing). Indispensable for surface-area mapping and exposed-service discovery.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "shodan_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "shodan_search failed");
    return res.output;
  },
});
