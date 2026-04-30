import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

// Tavily — answer-synthesis search.
const tavilyInput = z.object({
  query: z.string().min(2),
  search_depth: z.enum(["basic", "advanced"]).default("advanced"),
  limit: z.number().int().min(1).max(20).default(8),
  include_answer: z.boolean().default(true),
  include_domains: z.array(z.string()).optional(),
  exclude_domains: z.array(z.string()).optional(),
});

toolRegistry.register({
  name: "tavily_search",
  description:
    "Tavily AI-search: returns a synthesized answer (when include_answer=true) plus the source URLs used. Designed for agentic LLM consumption — use this for 'what is X' / 'who is X' questions instead of raw HTML scraping. REQUIRES TAVILY_API_KEY.",
  inputSchema: tavilyInput,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tavily_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tavily_search failed");
    return res.output;
  },
});

// Perplexity — citation-grounded LLM answer.
const pplxInput = z.object({
  query: z.string().min(2),
  model: z.string().default("sonar").describe("Perplexity Sonar model. Options: sonar, sonar-pro, sonar-reasoning, sonar-reasoning-pro"),
  system: z.string().optional().describe("Override the default OSINT-analyst system prompt"),
});

toolRegistry.register({
  name: "perplexity_search",
  description:
    "Query Perplexity Sonar — online LLM that synthesizes an answer with real-time web access and citations. Best for 'explain who X is' or 'what is the latest on Y' style queries that need fact-grounded synthesis (not raw URLs). REQUIRES PERPLEXITY_API_KEY.",
  inputSchema: pplxInput,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "perplexity_search",
      input: i,
      timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "perplexity_search failed");
    return res.output;
  },
});

// Grok — live X/Twitter access.
const grokInput = z.object({
  query: z.string().min(3).describe("Natural-language query about X content (e.g. 'find recent tweets from @username about AI', 'what is @username posting about?')"),
  model: z.string().default("grok-4-latest").describe("Grok model — grok-4-latest, grok-3-mini, etc."),
  system: z.string().optional(),
});

toolRegistry.register({
  name: "grok_x_search",
  description:
    "Query Grok (xAI) for live X/Twitter content — Grok has first-party platform access to X data. The ONLY credible programmatic path to current X content since snscrape died and the X API moved behind a $100+/mo paywall. Phrase queries naturally: 'what is @paulg posting about', 'find tweets from @anthropic about Claude'. REQUIRES XAI_API_KEY.",
  inputSchema: grokInput,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "grok_x_search",
      input: i,
      timeoutMs: 100_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "grok_x_search failed");
    return res.output;
  },
});
