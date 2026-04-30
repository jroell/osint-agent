import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  shop: z.string().min(3).describe("Shopify store URL or domain (e.g. 'allbirds.com')"),
  limit: z.number().int().min(1).max(250).default(50),
  page: z.number().int().min(1).default(1),
});

toolRegistry.register({
  name: "shopify_storefront_extract",
  description:
    "**Underexploited e-commerce OSINT goldmine** — every Shopify storefront exposes `/products.json` publicly (free, no auth, no rate limit at reasonable scale). Returns: complete product catalog with titles + descriptions + variants + SKUs + prices + compare_at_price (MSRP), vendor + product_type taxonomy, tags (often including INTERNAL TAXONOMIES like `brand::carbon-score => 5.9` that the brand never intended to expose), out-of-stock signals (popular SKUs), discount detection. Use cases: competitive pricing intel, counterfeit detection, supply-chain mapping, inventory inference. Returns is_shopify=false for non-Shopify stores.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "shopify_storefront_extract", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "shopify_storefront_extract failed");
    return res.output;
  },
});
