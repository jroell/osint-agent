import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  text: z.string().optional().describe("Any string (most useful for domains)"),
  domain: z.string().optional().describe("Alias for `text` — semantic clarity for domain checks"),
}).refine(d => d.text || d.domain, { message: "text or domain required" });

toolRegistry.register({
  name: "unicode_homoglyph_normalize",
  description:
    "**IDN homoglyph attack detector — pure-local, sub-millisecond.** Given a string (typically a domain), maps every non-ASCII character through a confusables table and reports: ASCII-normalized form, every flagged character with codepoint+script+ASCII-lookalike, unique scripts present, and a verdict (safe / suspicious / likely-attack). Mixed-script ASCII + Cyrillic/Greek = canonical IDN homoglyph attack pattern (e.g. `аnthropic.com` with Cyrillic а vs `anthropic.com` with Latin a — visually identical, different DNS resolution). Pair with `typosquat_scan` and `ct_brand_watch` to confirm whether candidate lookalikes are real homoglyph attacks.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "unicode_homoglyph_normalize", input: i, timeoutMs: 5_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "unicode_homoglyph_normalize failed");
    return res.output;
  },
});
