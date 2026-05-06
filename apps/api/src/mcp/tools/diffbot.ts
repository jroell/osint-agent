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
    "Query Diffbot's Knowledge Graph (~10B entities: people, companies, products, articles, images, places). Returns raw entities plus a canonical graph envelope (`graph.entities`, `graph.relationships`, `graph.claims`, `graph.hard_to_find_leads`) so downstream ER/pathfinding can persist Diffbot as sourced evidence instead of treating it as a blob. DQL examples: `type:Person name:\"Linus Torvalds\"` returns full entity with employment history, education, social profiles, photo. REQUIRES DIFFBOT_API_KEY.",
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

const entityNetworkInput = z.object({
  query: z.string().min(3).optional().describe("Diffbot DQL. Example: `type:Organization name:\"Anthropic\"`."),
  name: z.string().min(2).optional().describe("Entity display name. Used to build `name:\"...\"` when query is not supplied."),
  type: z.enum(["Person", "Organization", "Article", "Product", "Image", "Place"]).optional()
    .describe("Optional type shortcut prepended when query does not already specify a type."),
  size: z.number().int().min(1).max(10).default(3).describe("How many matching seed entities to normalize into a graph."),
});

toolRegistry.register({
  name: "diffbot_entity_network",
  description:
    "Diffbot Knowledge Graph entity-network expander. Resolves a person, organization, article, product, image, or place and returns a canonical relationship graph: entities, relationships, claims, and hard-to-find leads such as founders, investors, board roles, subsidiaries, parents, acquisitions, locations, and public URIs. Use this when Diffbot should seed the internal OSINT graph with provenance-backed public-web relationships. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: entityNetworkInput,
  costMillicredits: 25,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "diffbot_entity_network",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "diffbot_entity_network failed");
    return res.output;
  },
});

const commonNeighborsInput = z.object({
  entity_a: z.string().min(2).describe("First person or organization name."),
  entity_b: z.string().min(2).describe("Second person or organization name."),
  type_a: z.enum(["Person", "Organization"]).default("Person"),
  type_b: z.enum(["Person", "Organization"]).default("Person"),
});

toolRegistry.register({
  name: "diffbot_common_neighbors",
  description:
    "Diffbot Knowledge Graph common-neighbor search with canonical relationship output. Finds hard-to-find relationship bridges between two people or organizations: shared employers, schools, boards, founded companies, investors, locations, and public URIs. Returns ranked connections plus a canonical graph envelope for persistence and corroboration. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: commonNeighborsInput,
  costMillicredits: 25,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "diffbot_common_neighbors",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "diffbot_common_neighbors failed");
    return res.output;
  },
});

const articleCoMentionsInput = z.object({
  entity_a: z.string().min(2).describe("First entity name to search for in public-web articles."),
  entity_b: z.string().min(2).describe("Second entity name to search for in public-web articles."),
  query: z.string().min(3).optional().describe("Optional custom Diffbot DQL article query. Defaults to Article text contains both entity names."),
  size: z.number().int().min(1).max(50).default(10),
});

toolRegistry.register({
  name: "diffbot_article_co_mentions",
  description:
    "Diffbot Knowledge Graph article co-mention graph. Searches public-web Article entities that mention two targets, then returns articles, MENTIONS relationships, claims, and canonical graph entities for corroborating weak or hard-to-find connections. Treat co-mentions as evidence to verify, not as proof of a direct relationship. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: articleCoMentionsInput,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "diffbot_article_co_mentions",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "diffbot_article_co_mentions failed");
    return res.output;
  },
});
