import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3).describe("Subdomain to check for dangling-CNAME / takeover candidates"),
});

toolRegistry.register({
  name: "takeover_check",
  description:
    "Check whether a subdomain is a candidate for takeover (dangling CNAME pointing at an unclaimed third-party service like GitHub Pages, Heroku, S3, Shopify, Fastly, etc.). Returns matched fingerprints with vulnerable=true when the service body marker confirms the resource is unclaimed. Investigative reconnaissance only — never claim a service without explicit authorization from the domain owner.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "takeover_check",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "takeover_check failed");
    return res.output;
  },
});
