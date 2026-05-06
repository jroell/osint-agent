import { z } from "zod";
import { toolRegistry } from "./instance";
import {
  browserbaseFetch,
  browserbaseSearch,
  classifyFetchForEscalation,
  createBrowserbaseSession,
} from "../../browserbase/client";

const input = z.object({
  mode: z.enum(["auto", "search", "fetch"]).default("auto"),
  query: z.string().min(1).optional().describe("Search query for Browserbase Search."),
  url: z.string().url().optional().describe("URL to fetch through Browserbase Fetch."),
  num_results: z.number().int().min(1).max(25).default(5),
  fetch_top_n: z.number().int().min(0).max(10).default(3),
  min_content_bytes: z.number().int().min(0).max(50_000).default(500),
  proxies: z.boolean().default(true).describe("Route Browserbase Fetch/session traffic through Browserbase proxies."),
  escalate_to_browser: z.boolean().default(true).describe("Create Browserbase sessions for blocked or JS-only pages."),
  allow_redirects: z.boolean().default(true),
  allow_insecure_ssl: z.boolean().default(false),
});

type RetrievalInput = z.infer<typeof input>;

async function fetchAndClassify(i: RetrievalInput, url: string) {
  const fetched = await browserbaseFetch({
    url,
    proxies: i.proxies,
    allowRedirects: i.allow_redirects,
    allowInsecureSsl: i.allow_insecure_ssl,
  });
  const decision = classifyFetchForEscalation(fetched, i.min_content_bytes);
  const session = decision.blocked && i.escalate_to_browser
    ? await createBrowserbaseSession({
        url,
        proxies: i.proxies,
        instruction: "Render and inspect this page because lightweight fetch was blocked or incomplete.",
        metadata: { tool: "browserbase_retrieve", escalation_reasons: decision.reasons },
      })
    : undefined;

  return {
    url,
    fetch: fetched,
    escalation: {
      required: decision.blocked,
      reasons: decision.reasons,
      session,
    },
  };
}

toolRegistry.register({
  name: "browserbase_retrieve",
  description:
    "**Browserbase retrieval escalation — REQUIRES BROWSERBASE_API_KEY, and BROWSERBASE_PROJECT_ID when browser escalation is enabled.** Uses Browserbase Search and Fetch first, then creates real Chrome sessions for blocked, CAPTCHA-gated, JS-only, or short-content pages. Use when search/crawl APIs are blocked or need Browserbase-backed provenance. Outputs fetch evidence, escalation reasons, session metadata, and typed entities.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i) => {
    if (!i.url && !i.query) throw new Error("input.url or input.query required");
    if (i.mode === "search" && !i.query) throw new Error("input.query required for search mode");
    if (i.mode === "fetch" && !i.url) throw new Error("input.url required for fetch mode");

    const started = Date.now();
    const searched = i.query ? await browserbaseSearch(i.query, i.num_results) : undefined;
    const urls = i.url
      ? [i.url]
      : (searched?.results ?? []).slice(0, i.fetch_top_n).map((r) => r.url).filter(Boolean);
    const pages = i.mode === "search" ? [] : await Promise.all(urls.map((url) => fetchAndClassify(i, url)));

    const entities = [
      ...(searched?.results ?? []).map((r) => ({
        kind: "search_result",
        url: r.url,
        name: r.title || r.url,
        description: r.snippet,
        attributes: { source: "browserbase_search" },
      })),
      ...pages.map((p) => ({
        kind: p.escalation.session ? "browser_session" : "fetched_page",
        url: p.url,
        name: p.fetch.metadata?.title || p.url,
        description: p.escalation.required
          ? `Browser escalation required: ${p.escalation.reasons.join(", ")}`
          : "Browserbase Fetch returned usable content.",
        attributes: {
          source: "browserbase",
          status_code: p.fetch.statusCode,
          final_url: p.fetch.finalUrl,
          session_id: p.escalation.session?.id,
          connect_url: p.escalation.session?.connectUrl,
        },
      })),
    ];

    return {
      mode: i.mode,
      query: i.query,
      url: i.url,
      search: searched,
      pages,
      entities,
      highlight_findings: [
        searched ? `Browserbase Search returned ${searched.results.length} result(s).` : undefined,
        pages.length ? `Browserbase Fetch inspected ${pages.length} page(s).` : undefined,
        pages.some((p) => p.escalation.required)
          ? `Escalated ${pages.filter((p) => p.escalation.required).length} blocked/low-content page(s) to browser sessions.`
          : undefined,
      ].filter(Boolean),
      source: "browserbase.com",
      tookMs: Date.now() - started,
    };
  },
});
