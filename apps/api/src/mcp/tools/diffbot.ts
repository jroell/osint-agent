import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const extractInput = z.object({
  url: z.string().url(),
});

toolRegistry.register({
  name: "diffbot_extract",
  description:
    "Extract structured entities from any URL using Diffbot's Analyze API — auto-classifies the page (Article/Person/Company/Product/Image/etc.) and returns parsed fields specific to that type. The most reliable URL → structured-data extractor publicly available. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: extractInput,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "diffbot_extract",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "diffbot_extract failed");
    return res.output;
  },
});

const kgInput = z.object({
  query: z.string().min(3).describe("Diffbot DQL (Query Language). Examples: `type:Person name:\"Linus Torvalds\"`, `type:Organization name:\"Anthropic\"`, `type:Person employments.{employer.name:\"Vurvey Labs\"}`"),
  type: z.enum(["Person", "Organization", "Article", "Product", "Image", "Place"]).optional()
    .describe("Optional shortcut — prepended to query if it doesn't already specify a type"),
  size: z.number().int().min(1).max(50).default(10),
});

toolRegistry.register({
  name: "diffbot_kg_query",
  description:
    "Query Diffbot's Knowledge Graph (~10B entities: people, companies, products, articles, images, places). Highest-precision people/company enrichment in the catalog — Diffbot curates from hundreds of sources and links entities together with confidence scores. DQL examples: `type:Person name:\"Linus Torvalds\"` returns full entity with employment history, education, social profiles, photo. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: kgInput,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "diffbot_kg_query",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "diffbot_kg_query failed");
    return res.output;
  },
});
