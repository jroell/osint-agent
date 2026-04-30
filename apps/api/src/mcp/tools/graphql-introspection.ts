import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().optional().describe("Explicit GraphQL endpoint URL (POST query directly here). Mutually exclusive with `target`."),
  target: z.string().optional().describe("Bare domain (e.g. 'vurvey.app'). The tool will probe 11 common GraphQL paths in parallel and use whichever responds."),
}).refine(d => d.url || d.target, { message: "Provide either url or target" });

toolRegistry.register({
  name: "graphql_introspection",
  description:
    "Run the canonical IntrospectionQuery against a GraphQL endpoint. If introspection is enabled (often left on in dev/staging), recovers the FULL schema: every Query/Mutation/Subscription operation with arguments + return types, plus the entire type system. Highest-leverage GraphQL primitive — turns 'we found a /graphql endpoint' into 'here's every API capability'. Auto-probes 11 common paths (/graphql, /api/graphql, /v1/graphql, /graphql/system, etc.) when only a domain is supplied. Outputs deprecated-flagged operations + full argument signatures so the agent can compose follow-up queries.",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "graphql_introspection",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "graphql_introspection failed");
    return res.output;
  },
});
