/**
 * Tool-use harness for benchmarks. Wraps a curated subset of osint-agent's
 * search/lookup tools as native LLM tool definitions and runs an agent loop
 * (model proposes tool_use → harness executes → tool_result back to model →
 * loop until model emits a final answer or max iterations hit).
 *
 * Currently implements Anthropic's tool-use protocol; OpenAI/Gemini follow
 * the same JSON-schema tool definitions and need only a transport adapter.
 *
 * The "tools" here are direct API calls to the same upstream services
 * osint-agent's MCP tools wrap (Tavily, Perplexity, Firecrawl, Wikipedia,
 * Wikidata). They are a faithful proxy for "what would happen if the agent
 * called these via the MCP transport" — same providers, same data, just
 * without the credit-metering / event-log plumbing the production server adds.
 */

const TAVILY_KEY = () => process.env.TAVILY_API_KEY;
const PERPLEXITY_KEY = () => process.env.PERPLEXITY_API_KEY;
const FIRECRAWL_KEY = () => process.env.FIRECRAWL_API_KEY;
const ANTHROPIC_KEY = () => process.env.ANTHROPIC_API_KEY;

export interface ToolDef {
  name: string;
  description: string;
  input_schema: { type: "object"; properties: Record<string, unknown>; required?: string[] };
  invoke: (input: Record<string, unknown>) => Promise<unknown>;
}

const safeStringify = (x: unknown): string => {
  try {
    const s = JSON.stringify(x, null, 2);
    return s.length > 8000 ? s.slice(0, 8000) + "\n…[truncated]…" : s;
  } catch { return String(x).slice(0, 8000); }
};

export const TOOLS: ToolDef[] = [
  {
    name: "tavily_search",
    description: "AI-search engine that returns a synthesized answer + source URLs. Best for 'what is X' / 'who is X' factual questions where you want a direct answer plus citations. Less aggressive than raw web search; returns 5-10 high-relevance results.",
    input_schema: {
      type: "object",
      properties: {
        query: { type: "string", description: "Natural-language search query" },
        max_results: { type: "number", description: "1-20, default 8" },
      },
      required: ["query"],
    },
    invoke: async (i) => {
      const key = TAVILY_KEY(); if (!key) throw new Error("TAVILY_API_KEY not set");
      const res = await fetch("https://api.tavily.com/search", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ api_key: key, query: i.query, search_depth: "advanced", max_results: i.max_results ?? 8, include_answer: true }),
      });
      if (!res.ok) throw new Error(`tavily ${res.status}: ${(await res.text()).slice(0, 300)}`);
      return await res.json();
    },
  },
  {
    name: "perplexity_search",
    description: "Perplexity Sonar — citation-grounded LLM that browses the web and synthesizes an answer in one call. Best for 'what is the latest on X' or 'explain who X is' questions where you want a fact-grounded synthesis with sources.",
    input_schema: {
      type: "object",
      properties: {
        query: { type: "string" },
      },
      required: ["query"],
    },
    invoke: async (i) => {
      const key = PERPLEXITY_KEY(); if (!key) throw new Error("PERPLEXITY_API_KEY not set");
      const res = await fetch("https://api.perplexity.ai/chat/completions", {
        method: "POST",
        headers: { "content-type": "application/json", authorization: `Bearer ${key}` },
        body: JSON.stringify({ model: "sonar", messages: [{ role: "user", content: i.query }] }),
      });
      if (!res.ok) throw new Error(`perplexity ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { choices: Array<{ message: { content: string } }>; citations?: string[] };
      return { answer: data.choices[0]?.message?.content ?? "", citations: data.citations ?? [] };
    },
  },
  {
    name: "firecrawl_scrape",
    description: "Fetch a single URL and return its rendered text content (handles JS-heavy pages). Use AFTER finding a URL via tavily_search or perplexity_search to extract specific facts from the page.",
    input_schema: {
      type: "object",
      properties: {
        url: { type: "string", description: "Full http(s) URL to scrape" },
      },
      required: ["url"],
    },
    invoke: async (i) => {
      const key = FIRECRAWL_KEY(); if (!key) throw new Error("FIRECRAWL_API_KEY not set");
      const res = await fetch("https://api.firecrawl.dev/v1/scrape", {
        method: "POST",
        headers: { "content-type": "application/json", authorization: `Bearer ${key}` },
        body: JSON.stringify({ url: i.url, formats: ["markdown"], onlyMainContent: true }),
      });
      if (!res.ok) throw new Error(`firecrawl ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { data?: { markdown?: string; metadata?: unknown } };
      return { markdown: (data.data?.markdown ?? "").slice(0, 6000), metadata: data.data?.metadata };
    },
  },
  {
    name: "wikipedia_search",
    description: "Search English Wikipedia for article titles matching a query. Returns up to 10 candidate titles. Use this to find the right Wikipedia article before calling wikipedia_get.",
    input_schema: {
      type: "object",
      properties: { query: { type: "string" } },
      required: ["query"],
    },
    invoke: async (i) => {
      const url = `https://en.wikipedia.org/w/api.php?action=opensearch&format=json&search=${encodeURIComponent(String(i.query))}&limit=10`;
      const res = await fetch(url, { headers: { "user-agent": "osint-agent-benchmark/0.1 (https://github.com/jroell/osint-agent)" } });
      if (!res.ok) throw new Error(`wikipedia ${res.status}`);
      const data = (await res.json()) as [string, string[], string[], string[]];
      return { titles: data[1], descriptions: data[2], urls: data[3] };
    },
  },
  {
    name: "wikipedia_get",
    description: "Fetch a specific Wikipedia article's summary by exact title (use the title as returned by wikipedia_search). Returns extract, lat/lng if applicable, and the full URL.",
    input_schema: {
      type: "object",
      properties: { title: { type: "string", description: "Exact article title" } },
      required: ["title"],
    },
    invoke: async (i) => {
      const url = `https://en.wikipedia.org/api/rest_v1/page/summary/${encodeURIComponent(String(i.title))}`;
      const res = await fetch(url, { headers: { "user-agent": "osint-agent-benchmark/0.1" } });
      if (!res.ok) throw new Error(`wikipedia_get ${res.status}`);
      const d = (await res.json()) as { extract?: string; coordinates?: unknown; content_urls?: { desktop?: { page?: string } }; description?: string };
      return { extract: d.extract, description: d.description, coordinates: d.coordinates, url: d.content_urls?.desktop?.page };
    },
  },
  {
    name: "openalex_search",
    description: "Search OpenAlex (240M+ academic works, 100M+ authors) by free-text + filters. Best for finding academic papers / authors / institutions when you have constraints like 'paper about X published between Y-Z by author at institution W'. Filter syntax: 'publication_year:2004-2007,authorships.author.display_name.search:smith'. Returns list of works with author IDs, affiliations, abstracts, citation counts, and publication metadata. FREE, no key required.",
    input_schema: {
      type: "object",
      properties: {
        search: { type: "string", description: "Free-text search across title, abstract, authors" },
        filter: { type: "string", description: "Optional filter expression. Common: publication_year:2004-2007 / type:article / authorships.author.id:Axxxx / authorships.institutions.country_code:ZA" },
        per_page: { type: "number", description: "Results per page (max 25, default 10)" },
      },
      required: ["search"],
    },
    invoke: async (i) => {
      const params = new URLSearchParams({ search: String(i.search), per_page: String(Math.min(Number(i.per_page ?? 10), 25)) });
      if (i.filter) params.set("filter", String(i.filter));
      const res = await fetch(`https://api.openalex.org/works?${params}&mailto=osint-agent@batterii.com`, {
        headers: { "user-agent": "osint-agent-benchmark/0.1" },
      });
      if (!res.ok) throw new Error(`openalex ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { meta?: { count?: number }; results?: Array<{ id: string; title?: string; publication_year?: number; doi?: string; authorships?: Array<{ author: { display_name?: string; id?: string }; institutions?: Array<{ display_name?: string; country_code?: string }> }>; cited_by_count?: number; abstract_inverted_index?: unknown }> };
      const trimmed = (data.results ?? []).slice(0, 10).map((w) => ({
        id: w.id,
        title: w.title,
        year: w.publication_year,
        doi: w.doi,
        cited_by: w.cited_by_count,
        authors: (w.authorships ?? []).slice(0, 5).map((a) => ({
          name: a.author?.display_name,
          id: a.author?.id,
          institutions: (a.institutions ?? []).map((inst) => `${inst.display_name} (${inst.country_code ?? "?"})`),
        })),
      }));
      return { total_count: data.meta?.count, results: trimmed };
    },
  },
  {
    name: "openalex_authors_search",
    description: "Search OpenAlex AUTHORS index directly (not works). Best for 'find researchers in field X at country Y' style queries — when openalex_search by paper isn't surfacing the right author. Filter examples: 'last_known_institutions.country_code:ZA' (South Africa), 'works_count:>20' (productive researcher). Returns ranked author list with name, ORCID, current institution, total works. FREE, no key.",
    input_schema: {
      type: "object",
      properties: {
        search: { type: "string", description: "Free-text search across author display names + their topic concepts" },
        filter: { type: "string", description: "OpenAlex author filter, e.g. 'last_known_institutions.country_code:ZA,works_count:>10'. Use to narrow geographically when free-text returns too many results." },
        per_page: { type: "number", description: "Results per page, max 25, default 10" },
      },
      required: ["search"],
    },
    invoke: async (i) => {
      const params = new URLSearchParams({ search: String(i.search), per_page: String(Math.min(Number(i.per_page ?? 10), 25)) });
      if (i.filter) params.set("filter", String(i.filter));
      const res = await fetch(`https://api.openalex.org/authors?${params}&mailto=osint-agent@batterii.com`);
      if (!res.ok) throw new Error(`openalex_authors_search ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { meta?: { count?: number }; results?: Array<{ id: string; display_name?: string; orcid?: string; works_count?: number; cited_by_count?: number; last_known_institutions?: Array<{ display_name?: string; country_code?: string }>; x_concepts?: Array<{ display_name?: string; level?: number }> }> };
      return {
        total_count: data.meta?.count,
        authors: (data.results ?? []).map((a) => ({
          id: a.id,
          name: a.display_name,
          orcid: a.orcid,
          works_count: a.works_count,
          cited_by: a.cited_by_count,
          institutions: (a.last_known_institutions ?? []).map((inst) => `${inst.display_name} (${inst.country_code ?? "?"})`),
          top_topics: (a.x_concepts ?? []).filter((c) => (c.level ?? 0) >= 1).slice(0, 5).map((c) => c.display_name),
        })),
      };
    },
  },
  {
    name: "openalex_author_works",
    description: "Get every work published by a specific author (by OpenAlex author ID, e.g. 'A1234567890' or full URL). Use after openalex_search or openalex_authors_search finds candidate authors — this lets you VERIFY all their publications match the question's constraints (years, topics, sample sizes). Returns up to 50 works.",
    input_schema: {
      type: "object",
      properties: { author_id: { type: "string", description: "OpenAlex author id, like 'A1234567890' or 'https://openalex.org/A1234567890'" } },
      required: ["author_id"],
    },
    invoke: async (i) => {
      const id = String(i.author_id).replace(/^https?:\/\/openalex\.org\//, "");
      const res = await fetch(`https://api.openalex.org/works?filter=authorships.author.id:${id}&per_page=50&sort=publication_year:asc&mailto=osint-agent@batterii.com`);
      if (!res.ok) throw new Error(`openalex_author_works ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { meta?: { count?: number }; results?: Array<{ title?: string; publication_year?: number; doi?: string; type?: string; cited_by_count?: number }> };
      return {
        total_count: data.meta?.count,
        works: (data.results ?? []).map((w) => ({ year: w.publication_year, title: w.title, type: w.type, doi: w.doi, cited_by: w.cited_by_count })),
      };
    },
  },
  {
    name: "wikidata_sparql",
    description: "Run a SPARQL query against Wikidata's knowledge graph (~110M items including most academics, organizations, and topics). Best for STRUCTURED queries like 'find all people with PhD year 2004 in field of dental anthropology'. Use SELECT queries with LIMIT. Examples: SELECT ?person ?personLabel WHERE { ?person wdt:P31 wd:Q5; wdt:P512 ?degree . FILTER(REGEX(STR(?degree), 'PhD')) } LIMIT 10. FREE, no key.",
    input_schema: {
      type: "object",
      properties: { query: { type: "string", description: "Full SPARQL query. Always include LIMIT to avoid huge results." } },
      required: ["query"],
    },
    invoke: async (i) => {
      const url = `https://query.wikidata.org/sparql?format=json&query=${encodeURIComponent(String(i.query))}`;
      const res = await fetch(url, { headers: { accept: "application/sparql-results+json", "user-agent": "osint-agent-benchmark/0.1 (jroell@batterii.com)" } });
      if (!res.ok) throw new Error(`wikidata_sparql ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { results?: { bindings?: Array<Record<string, { value: string }>> } };
      const rows = (data.results?.bindings ?? []).slice(0, 50).map((b) => Object.fromEntries(Object.entries(b).map(([k, v]) => [k, v.value])));
      return { row_count: rows.length, rows };
    },
  },
  {
    name: "google_scholar_search",
    description: "Search Google Scholar via SerpAPI-style fallback (uses Tavily with site:scholar.google.com). Returns titles, snippets, and Scholar URLs for academic papers. Best for academic-paper search when you have title fragments or specific author + year combinations.",
    input_schema: {
      type: "object",
      properties: { query: { type: "string", description: "Search query — automatically scoped to scholar.google.com" } },
      required: ["query"],
    },
    invoke: async (i) => {
      const key = TAVILY_KEY(); if (!key) throw new Error("TAVILY_API_KEY not set");
      const res = await fetch("https://api.tavily.com/search", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ api_key: key, query: `${i.query} site:scholar.google.com OR site:researchgate.net OR site:academia.edu`, search_depth: "advanced", max_results: 10, include_answer: false }),
      });
      if (!res.ok) throw new Error(`google_scholar_search ${res.status}: ${(await res.text()).slice(0, 300)}`);
      return await res.json();
    },
  },
  {
    name: "crossref_search",
    description: "Search Crossref (130M+ scholarly works with full bibliographic metadata). Better than OpenAlex when you need DOI-level details, abstracts, or specific journal/publisher filtering. FREE, no key.",
    input_schema: {
      type: "object",
      properties: {
        query: { type: "string", description: "Free-text bibliographic search" },
        filter: { type: "string", description: "Optional Crossref filter, e.g. 'from-pub-date:2004,until-pub-date:2007,type:journal-article'" },
        rows: { type: "number", description: "Results, max 25, default 10" },
      },
      required: ["query"],
    },
    invoke: async (i) => {
      const params = new URLSearchParams({ query: String(i.query), rows: String(Math.min(Number(i.rows ?? 10), 25)) });
      if (i.filter) params.set("filter", String(i.filter));
      const res = await fetch(`https://api.crossref.org/works?${params}`, { headers: { "user-agent": "osint-agent-benchmark/0.1 (mailto:jroell@batterii.com)" } });
      if (!res.ok) throw new Error(`crossref ${res.status}: ${(await res.text()).slice(0, 300)}`);
      const data = (await res.json()) as { message?: { items?: Array<{ DOI?: string; title?: string[]; author?: Array<{ given?: string; family?: string; ORCID?: string; affiliation?: Array<{ name?: string }> }>; published?: { "date-parts"?: number[][] }; container?: string[]; abstract?: string }> } };
      return {
        results: (data.message?.items ?? []).map((w) => ({
          doi: w.DOI,
          title: (w.title ?? [])[0],
          year: (w.published?.["date-parts"]?.[0] ?? [])[0],
          journal: (w.container ?? [])[0],
          authors: (w.author ?? []).slice(0, 5).map((a) => ({
            name: `${a.given ?? ""} ${a.family ?? ""}`.trim(),
            orcid: a.ORCID,
            affiliations: (a.affiliation ?? []).map((af) => af.name).filter(Boolean),
          })),
          abstract: w.abstract?.slice(0, 500),
        })),
      };
    },
  },
];

const TOOL_BY_NAME = new Map(TOOLS.map((t) => [t.name, t]));

export interface AgentTrace {
  iterations: number;
  tool_calls: Array<{ name: string; input: Record<string, unknown>; output_summary: string; ok: boolean; took_ms: number }>;
  final_text: string;
  stop_reason: string;
  total_took_ms: number;
}

interface AnthropicContentBlock {
  type: "text" | "tool_use";
  text?: string;
  id?: string;
  name?: string;
  input?: Record<string, unknown>;
}

/**
 * Drive Anthropic Claude through an agent loop with the OSINT tool subset.
 * Returns the final text answer + a trace of every tool call made.
 */
export async function runAnthropicAgent(opts: {
  model: string;
  system: string;
  userPrompt: string;
  reasoningEffort?: "low" | "medium" | "high";
  maxIterations?: number;
  perCallMaxTokens?: number;
}): Promise<AgentTrace> {
  const key = ANTHROPIC_KEY(); if (!key) throw new Error("ANTHROPIC_API_KEY not set");
  const maxIter = opts.maxIterations ?? 10;
  const t0 = performance.now();
  const trace: AgentTrace = { iterations: 0, tool_calls: [], final_text: "", stop_reason: "", total_took_ms: 0 };

  type Msg = { role: "user" | "assistant"; content: unknown };
  const messages: Msg[] = [{ role: "user", content: opts.userPrompt }];

  for (let iter = 0; iter < maxIter; iter++) {
    trace.iterations = iter + 1;
    const body: Record<string, unknown> = {
      model: opts.model,
      max_tokens: opts.perCallMaxTokens ?? 16000,
      system: opts.system,
      tools: TOOLS.map((t) => ({ name: t.name, description: t.description, input_schema: t.input_schema })),
      messages,
    };
    if (opts.reasoningEffort && /(opus-4|sonnet-4|haiku-4)/i.test(opts.model)) {
      body.thinking = { type: "adaptive" };
      body.output_config = { effort: opts.reasoningEffort };
    }

    const res = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-api-key": key, "anthropic-version": "2023-06-01" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`anthropic ${res.status}: ${(await res.text()).slice(0, 400)}`);
    const data = (await res.json()) as { content: AnthropicContentBlock[]; stop_reason: string };
    trace.stop_reason = data.stop_reason;

    // Extract text + tool_use blocks
    const textBlocks = data.content.filter((b) => b.type === "text").map((b) => b.text ?? "");
    const toolUseBlocks = data.content.filter((b) => b.type === "tool_use");

    // Push assistant turn (preserve everything for tool-result threading)
    messages.push({ role: "assistant", content: data.content });

    if (toolUseBlocks.length === 0) {
      // No tool calls — model is done
      trace.final_text = textBlocks.join("\n");
      break;
    }

    // Execute each tool_use block in parallel and gather results
    const toolResults = await Promise.all(toolUseBlocks.map(async (tu) => {
      const tool = TOOL_BY_NAME.get(tu.name!);
      const tCallStart = performance.now();
      let output: unknown;
      let ok = true;
      try {
        if (!tool) { output = { error: `unknown tool ${tu.name}` }; ok = false; }
        else output = await tool.invoke(tu.input ?? {});
      } catch (e) {
        output = { error: (e as Error).message.slice(0, 400) };
        ok = false;
      }
      const summary = safeStringify(output).slice(0, 200);
      trace.tool_calls.push({
        name: tu.name!,
        input: tu.input ?? {},
        output_summary: summary,
        ok,
        took_ms: performance.now() - tCallStart,
      });
      return {
        type: "tool_result" as const,
        tool_use_id: tu.id!,
        content: safeStringify(output),
        ...(ok ? {} : { is_error: true }),
      };
    }));

    messages.push({ role: "user", content: toolResults });
  }

  trace.total_took_ms = performance.now() - t0;
  if (!trace.final_text && trace.iterations >= maxIter) {
    trace.final_text = "(max iterations reached without final answer)";
  }
  return trace;
}

/**
 * Drive Google Gemini through a function-calling agent loop with the OSINT
 * tool subset. Gemini's protocol uses `functionDeclarations` for tool defs
 * and surfaces tool calls as `functionCall` parts; tool results go back as
 * `functionResponse` parts in the next user turn.
 */
const GEMINI_KEY = () => process.env.GEMINI_API_KEY;

type GeminiPart =
  | { text: string }
  | { functionCall: { name: string; args: Record<string, unknown> } }
  | { functionResponse: { name: string; response: { content: string } } };

interface GeminiContent { role: "user" | "model"; parts: GeminiPart[]; }

export async function runGeminiAgent(opts: {
  model: string;
  system: string;
  userPrompt: string;
  maxIterations?: number;
  perCallMaxTokens?: number;
}): Promise<AgentTrace> {
  const key = GEMINI_KEY(); if (!key) throw new Error("GEMINI_API_KEY not set");
  const maxIter = opts.maxIterations ?? 10;
  const t0 = performance.now();
  const trace: AgentTrace = { iterations: 0, tool_calls: [], final_text: "", stop_reason: "", total_took_ms: 0 };

  const contents: GeminiContent[] = [
    { role: "user", parts: [{ text: opts.userPrompt }] },
  ];

  // Gemini parameter schema is OpenAPI-flavored (type, properties, required).
  const tools = [{
    functionDeclarations: TOOLS.map((t) => ({
      name: t.name,
      description: t.description,
      parameters: t.input_schema,
    })),
  }];

  for (let iter = 0; iter < maxIter; iter++) {
    trace.iterations = iter + 1;
    const body: Record<string, unknown> = {
      systemInstruction: { parts: [{ text: opts.system }] },
      tools,
      contents,
      generationConfig: {
        // Pro models burn 1.5-3k tokens on internal reasoning before emitting output.
        maxOutputTokens: opts.perCallMaxTokens ?? 16000,
        temperature: 0,
      },
    };

    const res = await fetch(
      `https://generativelanguage.googleapis.com/v1beta/models/${opts.model}:generateContent?key=${key}`,
      { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) },
    );
    if (!res.ok) throw new Error(`gemini ${res.status}: ${(await res.text()).slice(0, 400)}`);
    const data = (await res.json()) as {
      candidates?: Array<{ content: { parts: GeminiPart[] }; finishReason?: string }>;
    };
    const candidate = data.candidates?.[0];
    trace.stop_reason = candidate?.finishReason ?? "";
    const parts = candidate?.content?.parts ?? [];

    // Extract text + functionCall parts
    const textParts = parts.filter((p): p is { text: string } => "text" in p);
    const callParts = parts.filter((p): p is { functionCall: { name: string; args: Record<string, unknown> } } => "functionCall" in p);

    // Append assistant turn (preserve everything for next-round context)
    contents.push({ role: "model", parts });

    if (callParts.length === 0) {
      // No tool calls — model is done
      trace.final_text = textParts.map((p) => p.text).join("\n");
      break;
    }

    // Execute tool calls in parallel, gather functionResponse parts.
    const responseParts: GeminiPart[] = await Promise.all(callParts.map(async (cp) => {
      const tool = TOOL_BY_NAME.get(cp.functionCall.name);
      const tCallStart = performance.now();
      let output: unknown;
      let ok = true;
      try {
        if (!tool) { output = { error: `unknown tool ${cp.functionCall.name}` }; ok = false; }
        else output = await tool.invoke(cp.functionCall.args ?? {});
      } catch (e) {
        output = { error: (e as Error).message.slice(0, 400) };
        ok = false;
      }
      const summary = safeStringify(output).slice(0, 200);
      trace.tool_calls.push({
        name: cp.functionCall.name,
        input: cp.functionCall.args ?? {},
        output_summary: summary,
        ok,
        took_ms: performance.now() - tCallStart,
      });
      return {
        functionResponse: {
          name: cp.functionCall.name,
          response: { content: safeStringify(output) },
        },
      } as GeminiPart;
    }));

    contents.push({ role: "user", parts: responseParts });
  }

  trace.total_took_ms = performance.now() - t0;
  if (!trace.final_text && trace.iterations >= maxIter) {
    trace.final_text = "(max iterations reached without final answer)";
  }
  return trace;
}

