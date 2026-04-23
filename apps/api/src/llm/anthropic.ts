import Anthropic from "@anthropic-ai/sdk";
import { config } from "../config";
import type { LLMProvider, LLMRequest, LLMResponse } from "./gateway";

// Current (April 2026) Anthropic model pricing per 1M tokens, millicredits per token input/output.
// 1 millicredit = $0.00001 (so $3/M tokens = 30_000 / 1_000_000 = 0.030 millicredits per token, i.e. 30 micro-millicredits)
// Stored as millicredits * 1e6 per 1M tokens for integer math.
const PRICING = {
  "claude-opus-4-7":   { inPerM: 15_000_000, outPerM: 75_000_000 },
  "claude-sonnet-4-6": { inPerM:  3_000_000, outPerM: 15_000_000 },
  "claude-haiku-4-5":  { inPerM:    800_000, outPerM:  4_000_000 },
} as const;

export class AnthropicProvider implements LLMProvider {
  readonly id = "anthropic";
  private client: Anthropic;

  constructor(apiKey?: string) {
    this.client = new Anthropic({ apiKey: apiKey ?? config.anthropic.apiKey });
  }

  supports(model: string): boolean {
    return model in PRICING;
  }

  async complete(req: LLMRequest): Promise<LLMResponse> {
    const system = req.messages.find((m) => m.role === "system")?.content;
    const conv = req.messages.filter((m) => m.role !== "system");

    const response = await this.client.messages.create({
      model: req.model,
      max_tokens: req.maxTokens,
      temperature: req.temperature ?? 1.0,
      system,
      messages: conv.map((m) => ({ role: m.role as "user" | "assistant", content: m.content })),
    });

    const text = response.content
      .filter((b) => b.type === "text")
      .map((b) => (b.type === "text" ? b.text : ""))
      .join("");

    const pricing = PRICING[req.model as keyof typeof PRICING];
    const estimatedCostMillicredits = Math.ceil(
      (response.usage.input_tokens * pricing.inPerM + response.usage.output_tokens * pricing.outPerM) / 1_000_000,
    );

    return {
      content: text,
      modelUsed: req.model,
      inputTokens: response.usage.input_tokens,
      outputTokens: response.usage.output_tokens,
      estimatedCostMillicredits,
      fallbacksAttempted: [],
    };
  }
}
