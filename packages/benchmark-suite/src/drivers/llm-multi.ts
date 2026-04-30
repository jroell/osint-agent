/**
 * Multi-provider LLM driver. Speaks Anthropic Messages, OpenAI Chat
 * Completions, Gemini generateContent, and OpenRouter (OpenAI-compatible)
 * with one shared interface.
 *
 * Subject string format: `<provider>@<model_id>`
 *   anthropic@claude-opus-4-7
 *   openai@gpt-4o
 *   gemini@gemini-2.5-pro
 *   openrouter@deepseek/deepseek-chat
 *   openrouter@meta-llama/llama-3.3-70b-instruct
 *
 * Vision is supported when the model is vision-capable. Pass image bytes via
 * the `imageB64` + `imageMime` fields on `LLMRequest`. Non-vision models will
 * either ignore the image or fail — caller should pre-filter the panel.
 */

export type Provider = "anthropic" | "openai" | "gemini" | "openrouter";

export interface LLMRequest {
  prompt: string;
  /** Optional system prompt. Anthropic supports first-class; others get system role. */
  system?: string;
  /** Inline image (base64, no data: prefix). */
  imageB64?: string;
  imageMime?: string;
  /** Force JSON-shaped output where the provider supports a JSON-mode flag. */
  jsonOutput?: boolean;
  /** Bound max tokens for the cheap baselines. */
  maxTokens?: number;
  temperature?: number;
  /**
   * Reasoning effort for thinking-tier models. Mapping per provider:
   *   - OpenAI gpt-5.x: `reasoning_effort: "minimal"|"low"|"medium"|"high"` (top-level)
   *   - Anthropic claude-opus-4-7 / sonnet-4-6: `thinking: { type: "enabled", budget_tokens }`
   *   - Gemini 3.x Pro: built-in (no parameter). Bumps `maxOutputTokens` to give reasoning headroom.
   *   - OpenRouter: `reasoning: { effort }` passed through.
   * Non-reasoning models silently ignore.
   */
  reasoningEffort?: "minimal" | "low" | "medium" | "high";
}

export interface LLMResponse {
  text: string;
  /** Latency in ms for the API call only (not including queue/wait). */
  took_ms: number;
  /** Provider-reported token usage when available. */
  usage?: { input_tokens?: number; output_tokens?: number };
  /** Provider-specific raw object for inspection. */
  raw: unknown;
}

export interface LLM {
  subject: string;
  provider: Provider;
  model: string;
  vision: boolean;
  call(req: LLMRequest): Promise<LLMResponse>;
}

const ANTHROPIC_KEY = process.env.ANTHROPIC_API_KEY;
const OPENAI_KEY = process.env.OPENAI_API_KEY;
const GEMINI_KEY = process.env.GEMINI_API_KEY;
const OPENROUTER_KEY = process.env.OPENROUTER_API_KEY ?? process.env.OPEN_ROUTER_API_KEY;

function isVisionModel(model: string): boolean {
  // Heuristic — covers all 2026-current model families:
  //   - Anthropic Claude Opus/Sonnet/Haiku 4.x are all multimodal
  //   - OpenAI gpt-5.x and gpt-4.x families are multimodal
  //   - Gemini 3.x and 2.5 families are multimodal
  //   - OpenRouter VL / vision / multimodal-flagged models
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

/** Anthropic returns thinking blocks first (when extended thinking enabled), then text blocks. We only want the text. */
async function callAnthropic(model: string, req: LLMRequest): Promise<LLMResponse> {
  if (!ANTHROPIC_KEY) throw new Error("ANTHROPIC_API_KEY not set");
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
  // claude-opus-4-7 and newer Anthropic models reject the `temperature` field
  // (deterministic-only). Skip it for the Opus 4.7+ family.
  const acceptsTemperature = !/(opus-4-7|opus-4-8|sonnet-4-7|sonnet-4-8)/i.test(model);
  if (req.temperature !== undefined && acceptsTemperature) body.temperature = req.temperature;
  // Adaptive thinking for Opus 4.7 / Sonnet 4.6 / Haiku 4.5 families.
  // Per https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking:
  // the legacy `{type: "enabled", budget_tokens: N}` returns 400 on 4.7+.
  // Correct shape: thinking={type: "adaptive"}, output_config={effort: "..."}.
  if (req.reasoningEffort && /(opus-4|sonnet-4|haiku-4)/i.test(model)) {
    const effortMap = { minimal: "low", low: "low", medium: "medium", high: "high" } as const;
    body.thinking = { type: "adaptive" };
    body.output_config = { effort: effortMap[req.reasoningEffort] };
    body.max_tokens = Math.max(req.maxTokens ?? 1024, 16000);
  }

  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-api-key": ANTHROPIC_KEY,
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`anthropic ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as {
    content: Array<{ type: string; text?: string; thinking?: string }>;
    usage?: { input_tokens?: number; output_tokens?: number };
  };
  // text blocks only; thinking blocks are surfaced via `usage` not the answer.
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
  // OpenAI's gpt-5.x and o-series rejected `max_tokens` and require
  // `max_completion_tokens` instead. Keep the legacy name elsewhere.
  const usesNewMaxParam = baseUrl.includes("api.openai.com") && /^(gpt-5|o[134])/.test(model);
  if (req.maxTokens) {
    if (usesNewMaxParam) body.max_completion_tokens = req.maxTokens;
    else body.max_tokens = req.maxTokens;
  }
  // Reasoning models (gpt-5/o-series, deepseek-r1) reject temperature.
  const acceptsTemperature = !(usesNewMaxParam || /reasoning|r1/i.test(model));
  if (req.temperature !== undefined && acceptsTemperature) body.temperature = req.temperature;
  if (req.jsonOutput) body.response_format = { type: "json_object" };
  // Reasoning effort: gpt-5.x accepts top-level `reasoning_effort`. OpenRouter
  // normalizes via a `reasoning` object that gets routed to whichever provider.
  if (req.reasoningEffort) {
    if (baseUrl.includes("api.openai.com") && /^gpt-5/.test(model)) {
      body.reasoning_effort = req.reasoningEffort;
    } else if (baseUrl.includes("openrouter.ai")) {
      body.reasoning = { effort: req.reasoningEffort };
    }
  }

  const res = await fetch(`${baseUrl}/chat/completions`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${apiKey}`,
      "HTTP-Referer": "https://github.com/jroell/osint-agent",
      "X-Title": "osint-agent benchmark suite",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${baseUrl} ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as {
    choices: Array<{ message: { content: string | null; reasoning_content?: string; reasoning?: string } }>;
    usage?: { prompt_tokens?: number; completion_tokens?: number };
  };
  const msg = data.choices[0]?.message;
  // Some reasoning OSS models on OpenRouter put the answer in `reasoning_content`
  // or `reasoning` and leave `content` null. Fall back through both.
  const text = msg?.content ?? msg?.reasoning_content ?? msg?.reasoning ?? "";
  const usage = data.usage
    ? { input_tokens: data.usage.prompt_tokens, output_tokens: data.usage.completion_tokens }
    : undefined;
  return { text, took_ms: performance.now() - t0, usage, raw: data };
}

async function callOpenAI(model: string, req: LLMRequest): Promise<LLMResponse> {
  if (!OPENAI_KEY) throw new Error("OPENAI_API_KEY not set");
  return callOpenAICompat("https://api.openai.com/v1", OPENAI_KEY, model, req);
}

async function callOpenRouter(model: string, req: LLMRequest): Promise<LLMResponse> {
  if (!OPENROUTER_KEY) throw new Error("OPENROUTER_API_KEY not set");
  return callOpenAICompat("https://openrouter.ai/api/v1", OPENROUTER_KEY, model, req);
}

async function callGemini(model: string, req: LLMRequest): Promise<LLMResponse> {
  if (!GEMINI_KEY) throw new Error("GEMINI_API_KEY not set");
  const t0 = performance.now();
  const parts: Array<unknown> = [{ text: req.prompt }];
  if (req.imageB64) {
    parts.push({ inline_data: { mime_type: req.imageMime ?? "image/jpeg", data: req.imageB64 } });
  }
  // Gemini 2.5 Pro burns its first ~1500 output tokens on internal reasoning
  // before emitting a response. Bumping the floor to 2048 keeps short answers
  // from getting silently truncated.
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
    `https://generativelanguage.googleapis.com/v1beta/models/${model}:generateContent?key=${GEMINI_KEY}`,
    { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) },
  );
  if (!res.ok) throw new Error(`gemini ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as { candidates?: Array<{ content: { parts: Array<{ text: string }> } }> };
  const text = data.candidates?.[0]?.content?.parts?.map((p) => p.text ?? "").join("") ?? "";
  return { text, took_ms: performance.now() - t0, raw: data };
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
        case "anthropic":
          return callAnthropic(model, req);
        case "openai":
          return callOpenAI(model, req);
        case "openrouter":
          return callOpenRouter(model, req);
        case "gemini":
          return callGemini(model, req);
      }
    },
  };
}

/**
 * Curated 2026 SOTA panels.
 *
 * Cross-referenced 2026-04-29 against:
 *   - artificialanalysis.ai/leaderboards/models (Intelligence Index)
 *   - arena.ai/leaderboard (Arena ELO)
 *   - artificialanalysis.ai open-weights filter
 *
 * The previous panels (gpt-4o, gemini-2.5, deepseek-v3, llama-3.3, qwen-2.5)
 * are 12-18 months stale by 2026 SOTA standards. This file replaces them.
 */

/** Curated 2026 SOTA panel for text-only benchmarks (BrowseComp, GAIA, etc.). */
export const SOTA_TEXT_PANEL = [
  // Frontier closed-source — top 7 by Intelligence Index / Arena ELO
  "openai@gpt-5.5",                               // II 60 (top)
  "openai@gpt-5.4",                               // II 57
  "anthropic@claude-opus-4-7",                    // II 57, Arena 1503
  "anthropic@claude-sonnet-4-6",                  // II 52
  "gemini@gemini-3.1-pro-preview",                // II 57, Arena 1493
  "gemini@gemini-3-pro-preview",                  // Arena 1486
  "openrouter@x-ai/grok-4.20",                    // Arena #9
  // Cost-baseline lower-tier (still 2026-current)
  "openai@gpt-5.4-mini",
  "anthropic@claude-haiku-4-5",
  "gemini@gemini-3.1-flash-lite-preview",
  // SOTA open-weights (top 6 from artificialanalysis open-weights filter)
  "openrouter@moonshotai/kimi-k2.6",              // II 54 (top OSS)
  "openrouter@xiaomi/mimo-v2.5-pro",              // II 54
  "openrouter@deepseek/deepseek-v4-pro",          // II 52
  "openrouter@qwen/qwen3.6-max-preview",          // II 52
  "openrouter@z-ai/glm-5.1",                      // II 51
  "openrouter@minimax/minimax-m2.7",              // II 50
];

/** Curated 2026 SOTA panel for vision benchmarks (YFCC4k, geolocation). */
export const SOTA_VISION_PANEL = [
  // Frontier vision-capable
  "anthropic@claude-opus-4-7",
  "anthropic@claude-sonnet-4-6",
  "openai@gpt-5.5",
  "openai@gpt-5.4",
  "gemini@gemini-3.1-pro-preview",
  "gemini@gemini-3-pro-preview",
  "gemini@gemini-3-flash-preview",
  "openrouter@x-ai/grok-4.20",
  "openrouter@x-ai/grok-4",
  // SOTA open-weights with image input (verified via OpenRouter modalities API)
  "openrouter@moonshotai/kimi-k2.6",
  "openrouter@xiaomi/mimo-v2.5",                  // text+image+audio+video
  "openrouter@qwen/qwen3.6-plus",                 // text+image+video
  "openrouter@qwen/qwen3.6-flash",
  "openrouter@z-ai/glm-5v-turbo",
  "openrouter@meta-llama/llama-4-maverick",
  "openrouter@mistralai/pixtral-large-2411",
];
