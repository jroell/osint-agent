import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url(),
  follow_redirects: z.boolean().default(true),
  timeout_ms: z.number().int().min(1000).max(60000).default(15000),
});

toolRegistry.register({
  name: "http_probe",
  description:
    "Fetch a URL and return tech-fingerprint data: status, title, server/x-powered-by/cf-ray-style headers, technology hints (cms/framework/edge), favicon hash, redirect chain, and TLS subject/issuer. Use for fast tech-stack reconnaissance against a known host.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "http_probe",
      input: i,
      timeoutMs: i.timeout_ms + 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "http_probe failed");
    return res.output;
  },
});
