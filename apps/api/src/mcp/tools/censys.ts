import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(1).describe("Censys search query (e.g. 'services.tls.certificates.leaf_data.subject.common_name: example.com')"),
  limit: z.number().int().min(1).max(100).default(50),
});

toolRegistry.register({
  name: "censys_search",
  description:
    "Query Censys's internet-wide scan database. Complementary to Shodan — different scanner topology, often surfaces hosts the other misses. REQUIRES CENSYS_API_ID + CENSYS_API_SECRET env vars (free tier: 250 queries/month, https://search.censys.io/account/api).",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "censys_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "censys_search failed");
    return res.output;
  },
});
