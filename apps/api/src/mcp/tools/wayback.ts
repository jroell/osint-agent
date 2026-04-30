import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().min(3),
  limit: z.number().int().min(1).max(500).default(50),
  match_type: z.enum(["exact", "prefix", "host", "domain"]).default("exact"),
});

toolRegistry.register({
  name: "wayback_history",
  description:
    "List archived snapshots of a URL from the Internet Archive's Wayback Machine (CDX API). Free, no key. Each snapshot includes timestamp, MIME type, status code, content digest, and a direct archive_url. Excellent for 'what did this page look like' queries and historical subdomain/path discovery.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wayback_history",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wayback_history failed");
    return res.output;
  },
});
