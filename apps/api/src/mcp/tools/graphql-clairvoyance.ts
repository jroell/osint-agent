import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("GraphQL endpoint URL (e.g. 'https://api.example.com/graphql') or bare domain (assumes /graphql)"),
  wordlist: z.array(z.string()).optional().describe("Custom bait field names to probe. Defaults to a curated 130-word identity/auth/admin/data list."),
  max_baits: z.number().int().min(10).max(2000).optional().describe("Cap on bait probes. Default 200."),
  probe_mutations: z.boolean().optional().describe("Also probe mutation root (doubles request count). Default true."),
  skip_introspection_check: z.boolean().optional().describe("Skip the initial introspection probe. Default false."),
});

toolRegistry.register({
  name: "graphql_clairvoyance",
  description:
    "**SOTA bug-bounty technique — recovers GraphQL schema fragments when introspection is disabled.** Bombards the endpoint with bogus field names from a wordlist and parses the 'Did you mean X?' suggestion errors that most servers leak even when introspection is locked. Reference impl: github.com/nikitastupin/clairvoyance — featured in OWASP API Security Top 10 (2023). Returns discovered query/mutation field names + the types they belong to. Pair with `graphql_introspection`: try introspection first (faster, complete), fall back to clairvoyance when locked. Curated default wordlist skews toward identity/auth/admin/data fields (highest ER signal). Cost: O(N) HTTP requests; default 200 baits × 2 (query+mutation) = 400 requests at 8x concurrency.",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "graphql_clairvoyance",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "graphql_clairvoyance failed");
    return res.output;
  },
});
