import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(1).describe("IPv4/IPv6 address (e.g. 1.1.1.1) or ASN (e.g. AS15169 or 15169)"),
});

toolRegistry.register({
  name: "asn_lookup",
  description:
    "Resolve an IP to its origin ASN with org name, country, and announced prefixes; or resolve an ASN to its org metadata. Useful for attributing an IP to a hosting provider or enumerating an org's IP space. Free, no API key. Source: BGPView.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "asn_lookup",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "asn_lookup failed");
    return res.output;
  },
});
