import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  ip: z.string().describe("IPv4 or IPv6 address to reverse-resolve"),
});

toolRegistry.register({
  name: "reverse_dns",
  description:
    "Live PTR lookup for an IP address — returns hostnames currently bound to it. Note: historical reverse-DNS (every name that ever pointed at this IP) requires a paid passive-DNS provider and is not available in the free-tier implementation.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "reverse_dns",
      input: i,
      timeoutMs: 10_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reverse_dns failed");
    return res.output;
  },
});
