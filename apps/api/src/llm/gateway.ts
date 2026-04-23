export type LLMRole = "system" | "user" | "assistant";

export interface LLMMessage {
  role: LLMRole;
  content: string;
}

export interface LLMRequest {
  messages: LLMMessage[];
  model: string;                    // e.g. "claude-sonnet-4-6", "claude-haiku-4-5"
  maxTokens: number;
  temperature?: number;
  // Soft cost ceiling in millicredits; if estimated spend would exceed, Gateway falls back.
  costCeilingMillicredits?: number;
  // Fallback chain of model IDs to try if primary fails or exceeds ceiling.
  fallbackChain?: string[];
  // Tag this call so parallel benchmark runs can log to the eval store (Phase 2+).
  benchmarkTag?: string;
}

export interface LLMResponse {
  content: string;
  modelUsed: string;
  inputTokens: number;
  outputTokens: number;
  estimatedCostMillicredits: number;
  // Non-empty if the primary model failed/skipped and a fallback served the request.
  fallbacksAttempted: string[];
}

export interface LLMProvider {
  readonly id: string;                   // "anthropic" | "openrouter" | "byok:anthropic"
  supports(model: string): boolean;
  complete(req: LLMRequest): Promise<LLMResponse>;
}

export class LLMGateway {
  private providers: LLMProvider[] = [];

  register(p: LLMProvider): void {
    this.providers.push(p);
  }

  async complete(req: LLMRequest): Promise<LLMResponse> {
    const tried: string[] = [];
    const chain = [req.model, ...(req.fallbackChain ?? [])];

    let lastErr: unknown;
    for (const model of chain) {
      const provider = this.providers.find((p) => p.supports(model));
      if (!provider) {
        tried.push(`${model}(no-provider)`);
        continue;
      }
      try {
        const result = await provider.complete({ ...req, model });
        return { ...result, fallbacksAttempted: tried };
      } catch (e) {
        tried.push(`${model}(${(e as Error).message})`);
        lastErr = e;
      }
    }
    throw new Error(`All models in chain failed: ${tried.join(", ")}`);
  }
}
