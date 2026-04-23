import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  url: z.string().url(),
  method: z.enum(["GET", "POST"]).default("GET"),
  impersonate: z.enum(["chrome", "firefox", "safari", "safari_ios", "edge", "okhttp"]).default("chrome"),
  headers: z.record(z.string(), z.string()).optional(),
  body: z.string().optional(),
  timeout_ms: z.number().int().min(1000).max(60000).default(15000),
});

toolRegistry.register({
  name: "stealth_http_fetch",
  description:
    "Fetch a URL with JA4+ TLS fingerprint impersonation. Bypasses a large fraction of Cloudflare and DataDome protections without launching a browser.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "stealth_http_fetch",
      input: i,
      timeoutMs: i.timeout_ms,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "stealth_http failed");
    return res.output;
  },
});
