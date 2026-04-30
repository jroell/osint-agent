import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
  include_raw: z.boolean().default(false).describe("Include raw cert rows alongside the deduped subdomain list"),
  max_raw: z.number().int().min(1).max(5000).default(500),
});

toolRegistry.register({
  name: "cert_transparency_query",
  description:
    "Query Certificate Transparency logs (crt.sh) for every certificate ever issued under a domain. Excellent passive subdomain discovery — finds names that DNS-only methods miss. Free, no API key.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "cert_transparency_query",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "cert_transparency_query failed");
    return res.output;
  },
});
