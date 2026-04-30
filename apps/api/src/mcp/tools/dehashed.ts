import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("DeHashed search syntax (e.g. 'email:victim@example.com', 'domain:example.com', 'username:foo')"),
  limit: z.number().int().min(1).max(10000).default(100),
});

toolRegistry.register({
  name: "dehashed_search",
  description:
    "Search the DeHashed breach-records database. REQUIRES DEHASHED_API_KEY + DEHASHED_EMAIL env vars (paid, https://dehashed.com/api). Returns leaked records — use only for authorized investigations.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "dehashed_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "dehashed_search failed");
    return res.output;
  },
});
