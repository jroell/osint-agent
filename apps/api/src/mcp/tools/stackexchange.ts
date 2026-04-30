import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Display name to search across Stack Exchange Network"),
  limit_per_site: z.number().int().min(1).max(20).default(5),
  sites: z.array(z.string()).optional().describe("Override the default site list (stackoverflow, superuser, serverfault, askubuntu, unix, security, crypto, reverseengineering, meta)"),
});

toolRegistry.register({
  name: "stackexchange_user",
  description:
    "Search the Stack Exchange Network in parallel across the 9 highest-traffic sites for a username (display-name match). Returns reputation, profile, location, bio, and verified websites. Free, 300/day unauth; STACKEXCHANGE_API_KEY (free) raises to 10,000/day. Partial-result tolerant — single-site failures don't kill the whole query.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "stackexchange_user",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "stackexchange_user failed");
    return res.output;
  },
});
