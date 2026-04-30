import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Domain (example.com) or IP address (1.1.1.1)"),
});

toolRegistry.register({
  name: "whois_query",
  description:
    "RDAP-based WHOIS lookup for a domain or IP. Returns registrar, registration/expiration dates, status, and nameservers. Free, no API key. Source: rdap.org (IANA bootstrap).",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "whois_query",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "whois_query failed");
    return res.output;
  },
});
