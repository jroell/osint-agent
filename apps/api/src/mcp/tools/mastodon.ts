import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(1).describe("Username, with or without @ (e.g. 'alice' or 'alice@example.org')"),
  instances: z.array(z.string()).optional().describe("Override the default 5-instance list (mastodon.social, hachyderm.io, infosec.exchange, fosstodon.org, mas.to)"),
  per_instance_timeout_s: z.number().int().min(2).max(30).default(6),
});

toolRegistry.register({
  name: "mastodon_user_lookup",
  description:
    "Search the federated Mastodon network for a username across 5 high-traffic instances IN PARALLEL. Aggregates and deduplicates matches; partial-result tolerant — if mastodon.social is rate-limiting, the other 4 still respond. This is the textbook 'fallback for flaky services' pattern: federation makes single-instance failures gracefully degrade.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "mastodon_user_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "mastodon_user_lookup failed");
    return res.output;
  },
});
