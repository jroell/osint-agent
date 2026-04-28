import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
});

toolRegistry.register({
  name: "subfinder_passive",
  description:
    "Passive subdomain enumeration via ProjectDiscovery's subfinder library across 30+ public sources (crt.sh, HackerTarget, etc.). No active probing.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "subfinder_passive",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "subfinder failed");
    return res.output;
  },
});
