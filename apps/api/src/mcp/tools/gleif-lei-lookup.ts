import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Company name fuzzy search OR exact 20-char LEI code"),
  limit: z.number().int().min(1).max(50).default(10),
});

toolRegistry.register({
  name: "gleif_lei_lookup",
  description:
    "**Corporate intelligence ER — Global Legal Entity Identifier registry.** GLEIF is the international standard registry for legal entities (every regulated financial entity has an LEI — banks, public companies, hedge funds, etc). Free public API, no key. Returns: 20-char LEI code, exact legal name + alternative names, legal form (LLC, PBC, GmbH, etc.), HQ + legal addresses, parent LEI (corporate hierarchy walk), BIC/SWIFT codes, registration metadata. Use cases: confirm exact legal entity name (e.g. 'Anthropic' → 'Anthropic, PBC'), discover parent companies, cross-reference with mobile_app_lookup `sellerName` and github_code_search `commit author` fields.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gleif_lei_lookup", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gleif_lei_lookup failed");
    return res.output;
  },
});
