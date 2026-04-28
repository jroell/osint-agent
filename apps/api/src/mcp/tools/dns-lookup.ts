import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
});

toolRegistry.register({
  name: "dns_lookup_comprehensive",
  description:
    "Resolve A / AAAA / MX / TXT / NS / CNAME records for a domain in parallel. Returns structured results with per-record-type errors on partial failure.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "dns_lookup_comprehensive",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "dns lookup failed");
    return res.output;
  },
});
