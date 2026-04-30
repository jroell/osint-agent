/**
 * Unified multi-provider LLM driver for the consultation panel.
 *
 * Speaks Anthropic Messages, OpenAI Chat Completions, Gemini generateContent,
 * and OpenRouter (OpenAI-compatible) with one shared interface. Mirrors the
 * benchmark-suite driver but lives in the API package so the MCP tools and
 * panel registry can use it directly.
 *
 * Subject string format: `<provider>@<model_id>`
 *   anthropic@claude-opus-4-7
 *   openai@gpt-5.5
 *   gemini@gemini-3.1-pro-preview
 *   openrouter@moonshotai/kimi-k2.6
 *
 * The 2026 SOTA panel selection lives in `panel.ts`; this module just speaks
 * to whichever models the panel registry has flagged available.
 */

export type Provider = "anthropic" | "openai" | "gemini" | "openrouter";

export interface LLMRequest {
  prompt: string;
  system?: string;
  imageB64?: string;
  imageMime?: string;
  jsonOutput?: boolean;
  maxTokens?: number;
  temperature?: number;
}

export interface LLMResponse {
  text: string;
  took_ms: number;
  /** Provider-reported usage when available — used for true-up cost accounting. */
  usage?: { input_tokens?: number; output_tokens?: number };
  raw: unknown;
}

export interface LLM {
  subject: string;
  provider: Provider;
  model: string;
  vision: boolean;
  call(req: LLMRequest): Promise<LLMResponse>;
}

const ANTHROPIC_KEY = () => process.env.ANTHROPIC_API_KEY;
const OPENAI_KEY = () => process.env.OPENAI_API_KEY;
const GEMINI_KEY = () => process.env.GEMINI_API_KEY;
const OPENROUTER_KEY = () => process.env.OPENROUTER_API_KEY ?? process.env.OPEN_ROUTER_API_KEY;

export function providerHasKey(p: Provider): boolean {
  switch (p) {
    case "anthropic": return !!ANTHROPIC_KEY();
    case "openai": return !!OPENAI_KEY();
    case "gemini": return !!GEMINI_KEY();
    case "openrouter": return !!OPENROUTER_KEY();
  }
}

export function isVisionModel(model: string): boolean {
  return /(opus|sonnet|haiku|gpt-(5|4)|gemini-(2\.5|3)|vl|vision|pixtral|kimi-k2\.6|mimo-v2|qwen3\.6|glm-5v|llama-4|grok-4)/i.test(model);
}

function parseSubject(subject: string): { provider: Provider; model: string } {
  const m = subject.match(/^([a-z]+)@(.+)$/);
  if (!m) throw new Error(`bad subject "${subject}", expected "<provider>@<model>"`);
  const provider = m[1] as Provider;
  if (!["anthropic", "openai", "gemini", "openrouter"].includes(provider)) {
    throw new Error(`bad provider "${provider}"`);
  }
  return { provider, model: m[2]! };
}

async function callAnthropic(model: string, req: LLMRequest): Promise<LLMResponse> {
  const key = ANTHROPIC_KEY();
  if (!key) throw new Error("ANTHROPIC_API_KEY not set");
  const t0 = performance.now();
  const userContent: Array<unknown> = [{ type: "text", text: req.prompt }];
  if (req.imageB64) {
    userContent.unshift({
      type: "image",
      source: { type: "base64", media_type: req.imageMime ?? "image/jpeg", data: req.imageB64 },
    });
  }
  const body: Record<string, unknown> = {
    model,
    max_tokens: req.maxTokens ?? 1024,
    messages: [{ role: "user", content: userContent }],
  };
  if (req.system) body.system = req.system;
  // Newer Anthropic flagship models reject the `temperature` field.
  const acceptsTemperature = !/(opus-4-7|opus-4-8|sonnet-4-7|sonnet-4-8)/i.test(model);
  if (req.temperature !== undefined && acceptsTemperature) body.temperature = req.temperature;

  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-api-key": key,
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`anthropic ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as {
    content: Array<{ type: string; text?: string }>;
    usage?: { input_tokens?: number; output_tokens?: number };
  };
  const text = data.content.filter((b) => b.type === "text").map((b) => b.text ?? "").join("");
  return { text, took_ms: performance.now() - t0, usage: data.usage, raw: data };
}

async function callOpenAICompat(
  baseUrl: string,
  apiKey: string,
  model: string,
  req: LLMRequest,
): Promise<LLMResponse> {
  const t0 = performance.now();
  const messages: Array<unknown> = [];
  if (req.system) messages.push({ role: "system", content: req.system });
  if (req.imageB64) {
    messages.push({
      role: "user",
      content: [
        { type: "text", text: req.prompt },
        { type: "image_url", image_url: { url: `data:${req.imageMime ?? "image/jpeg"};base64,${req.imageB64}` } },
      ],
    });
  } else {
    messages.push({ role: "user", content: req.prompt });
  }
  const body: Record<string, unknown> = { model, messages };
  // gpt-5.x and o-series use `max_completion_tokens` not `max_tokens` and reject `temperature`.
  const usesNewParam = baseUrl.includes("api.openai.com") && /^(gpt-5|o[134])/.test(model);
  if (req.maxTokens) {
    if (usesNewParam) body.max_completion_tokens = req.maxTokens;
    else body.max_tokens = req.maxTokens;
  }
  const acceptsTemperature = !(usesNewParam || /reasoning|r1/i.test(model));
  if (req.temperature !== undefined && acceptsTemperature) body.temperature = req.temperature;
  if (req.jsonOutput) body.response_format = { type: "json_object" };

  const res = await fetch(`${baseUrl}/chat/completions`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${apiKey}`,
      "HTTP-Referer": "https://github.com/jroell/osint-agent",
      "X-Title": "osint-agent panel",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${baseUrl} ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as {
    choices: Array<{ message: { content: string | null; reasoning_content?: string; reasoning?: string } }>;
    usage?: { prompt_tokens?: number; completion_tokens?: number };
  };
  const msg = data.choices[0]?.message;
  // Some OpenRouter reasoning models leave content null and put answer in reasoning_content.
  const text = msg?.content ?? msg?.reasoning_content ?? msg?.reasoning ?? "";
  const usage = data.usage
    ? { input_tokens: data.usage.prompt_tokens, output_tokens: data.usage.completion_tokens }
    : undefined;
  return { text, took_ms: performance.now() - t0, usage, raw: data };
}

async function callOpenAI(model: string, req: LLMRequest): Promise<LLMResponse> {
  const key = OPENAI_KEY();
  if (!key) throw new Error("OPENAI_API_KEY not set");
  return callOpenAICompat("https://api.openai.com/v1", key, model, req);
}

async function callOpenRouter(model: string, req: LLMRequest): Promise<LLMResponse> {
  const key = OPENROUTER_KEY();
  if (!key) throw new Error("OPENROUTER_API_KEY not set");
  return callOpenAICompat("https://openrouter.ai/api/v1", key, model, req);
}

async function callGemini(model: string, req: LLMRequest): Promise<LLMResponse> {
  const key = GEMINI_KEY();
  if (!key) throw new Error("GEMINI_API_KEY not set");
  const t0 = performance.now();
  const parts: Array<unknown> = [{ text: req.prompt }];
  if (req.imageB64) {
    parts.push({ inline_data: { mime_type: req.imageMime ?? "image/jpeg", data: req.imageB64 } });
  }
  // Gemini 2.5/3.x Pro spend their first ~1500 output tokens on internal reasoning.
  const isPro = /pro/i.test(model);
  const minTokens = isPro ? 2048 : 256;
  const body: Record<string, unknown> = {
    contents: [{ role: "user", parts }],
    generationConfig: {
      ...(req.temperature !== undefined ? { temperature: req.temperature } : {}),
      maxOutputTokens: Math.max(req.maxTokens ?? 0, minTokens),
      ...(req.jsonOutput ? { response_mime_type: "application/json" } : {}),
    },
  };
  if (req.system) (body as { systemInstruction?: unknown }).systemInstruction = { parts: [{ text: req.system }] };

  const res = await fetch(
    `https://generativelanguage.googleapis.com/v1beta/models/${model}:generateContent?key=${key}`,
    { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) },
  );
  if (!res.ok) throw new Error(`gemini ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as {
    candidates?: Array<{ content: { parts: Array<{ text: string }> } }>;
    usageMetadata?: { promptTokenCount?: number; candidatesTokenCount?: number };
  };
  const text = data.candidates?.[0]?.content?.parts?.map((p) => p.text ?? "").join("") ?? "";
  const usage = data.usageMetadata
    ? { input_tokens: data.usageMetadata.promptTokenCount, output_tokens: data.usageMetadata.candidatesTokenCount }
    : undefined;
  return { text, took_ms: performance.now() - t0, usage, raw: data };
}

export function makeLLM(subject: string): LLM {
  const { provider, model } = parseSubject(subject);
  return {
    subject,
    provider,
    model,
    vision: isVisionModel(model),
    async call(req: LLMRequest): Promise<LLMResponse> {
      switch (provider) {
        case "anthropic": return callAnthropic(model, req);
        case "openai": return callOpenAI(model, req);
        case "openrouter": return callOpenRouter(model, req);
        case "gemini": return callGemini(model, req);
      }
    },
  };
}
