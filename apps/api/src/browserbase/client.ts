export type BrowserbaseSearchResult = {
  title?: string;
  url: string;
  snippet?: string;
};

export type BrowserbaseFetchResult = {
  id?: string;
  url: string;
  finalUrl?: string;
  statusCode?: number;
  headers?: Record<string, string>;
  content?: string;
  markdown?: string;
  metadata?: Record<string, unknown>;
};

export type BrowserbaseSessionResult = {
  id: string;
  status?: string;
  connectUrl?: string;
  seleniumRemoteUrl?: string;
  liveViewUrl?: string;
};

export type BlockDecision = {
  blocked: boolean;
  reasons: string[];
};

const BLOCK_PATTERNS = [
  /access denied/i,
  /attention required/i,
  /captcha/i,
  /cloudflare/i,
  /enable javascript/i,
  /just a moment/i,
  /robot check/i,
  /unusual traffic/i,
  /verify you are human/i,
];

function apiKey(): string {
  const key = process.env.BROWSERBASE_API_KEY;
  if (!key) throw new Error("BROWSERBASE_API_KEY required");
  return key;
}

function projectId(): string {
  const id = process.env.BROWSERBASE_PROJECT_ID;
  if (!id) throw new Error("BROWSERBASE_PROJECT_ID required for browser sessions");
  return id;
}

async function browserbaseRequest<T>(path: string, body: unknown, timeoutMs = 60_000): Promise<T> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(`https://api.browserbase.com${path}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-bb-api-key": apiKey(),
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    const text = await res.text();
    const parsed = text ? JSON.parse(text) : {};
    if (!res.ok) {
      const msg = typeof parsed?.message === "string" ? parsed.message : text.slice(0, 240);
      throw new Error(`Browserbase ${path} HTTP ${res.status}: ${msg}`);
    }
    return parsed as T;
  } finally {
    clearTimeout(timeout);
  }
}

export function classifyFetchForEscalation(fetchResult: BrowserbaseFetchResult, minContentBytes: number): BlockDecision {
  const reasons: string[] = [];
  const status = fetchResult.statusCode ?? 0;
  if ([401, 403, 407, 408, 409, 429, 451, 500, 502, 503, 504].includes(status)) {
    reasons.push(`status_${status}`);
  }

  const content = fetchResult.markdown || fetchResult.content || "";
  if (content.trim().length < minContentBytes) {
    reasons.push(`short_content_${content.trim().length}`);
  }
  for (const pattern of BLOCK_PATTERNS) {
    if (pattern.test(content)) {
      reasons.push(`matched_${pattern.source.toLowerCase().replaceAll("\\", "")}`);
      break;
    }
  }

  return { blocked: reasons.length > 0, reasons };
}

export async function browserbaseSearch(query: string, numResults: number): Promise<{
  requestId?: string;
  results: BrowserbaseSearchResult[];
}> {
  const response = await browserbaseRequest<{ requestId?: string; results?: BrowserbaseSearchResult[] }>(
    "/v1/search",
    { query, numResults },
    30_000,
  );
  return { requestId: response.requestId, results: response.results ?? [] };
}

export async function browserbaseFetch(input: {
  url: string;
  allowRedirects?: boolean;
  allowInsecureSsl?: boolean;
  proxies?: boolean;
  headers?: Record<string, string>;
}): Promise<BrowserbaseFetchResult> {
  const response = await browserbaseRequest<BrowserbaseFetchResult>("/v1/fetch", input, 45_000);
  return {
    ...response,
    url: response.url || input.url,
  };
}

export async function createBrowserbaseSession(input: {
  url?: string;
  instruction?: string;
  proxies?: boolean;
  contextId?: string;
  solveCaptchas?: boolean;
  keepAlive?: boolean;
  metadata?: Record<string, unknown>;
}): Promise<BrowserbaseSessionResult> {
  const metadata = {
    ...(input.metadata ?? {}),
    ...(input.url ? { target_url: input.url } : {}),
    ...(input.instruction ? { instruction: input.instruction } : {}),
  };
  return await browserbaseRequest<BrowserbaseSessionResult>(
    "/v1/sessions",
    {
      projectId: projectId(),
      keepAlive: input.keepAlive ?? false,
      proxies: input.proxies ?? false,
      contextId: input.contextId,
      userMetadata: metadata,
      browserSettings: {
        solveCaptchas: input.solveCaptchas ?? true,
        viewport: { width: 1280, height: 900 },
      },
    },
    30_000,
  );
}

export async function invokeBrowserbaseFunction(functionId: string, params: Record<string, unknown>): Promise<{
  id: string;
  functionId?: string;
  sessionId?: string;
  status?: string;
  results?: unknown;
}> {
  return await browserbaseRequest(`/v1/functions/${encodeURIComponent(functionId)}/invoke`, { params }, 30_000);
}
