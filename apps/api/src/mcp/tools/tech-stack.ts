import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url(),
  timeout_ms: z.number().int().min(1000).max(60000).default(15000),
});

toolRegistry.register({
  name: "tech_stack_fingerprint",
  description:
    "Identify the technology stack behind a URL (CMS, frameworks, analytics, CDNs, programming languages, web servers) using ProjectDiscovery's wappalyzergo against the open Wappalyzer fingerprint database (~3000 technologies). Complements http_probe with deeper categorization.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tech_stack_fingerprint",
      input: i,
      timeoutMs: i.timeout_ms + 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tech_stack_fingerprint failed");
    return res.output;
  },
});
