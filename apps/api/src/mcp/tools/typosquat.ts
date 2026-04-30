import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(4).describe("Apex domain to protect (e.g. 'vurvey.app')"),
  check_mx: z.boolean().default(true).describe("Also resolve MX records to flag mailbox-capable phishing infrastructure"),
  concurrency: z.number().int().min(1).max(200).default(40),
  max_candidates: z.number().int().min(50).max(5000).default(1500).describe("Cap on candidate domains generated; trims to highest-priority algorithms first when exceeded"),
});

toolRegistry.register({
  name: "typosquat_scan",
  description:
    "Generate plausible typosquat / homoglyph / lookalike domains for a target via 9 dnstwist-style algorithms (omission, transposition, repetition, qwerty-neighbor insertion+replacement, bitsquat, IDN homoglyph, TLD swap, hyphen insertion, subdomain split), then resolve each in parallel. Returns ONLY domains that actually resolve, with their A / AAAA / MX records. Flags IDN domains (Unicode homograph attacks) and MX-present domains (mailbox-capable = higher phishing threat). Pure-Go implementation, no external dependency. Compose with `favicon_pivot` (to confirm brand-favicon copying) and `http_probe` (to capture live title/server) for full phishing-confirmation chain.",
  inputSchema: input,
  costMillicredits: 12,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "typosquat_scan",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "typosquat_scan failed");
    return res.output;
  },
});
