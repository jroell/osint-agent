import { describe, it, expect, mock } from "bun:test";
import { LLMGateway, type LLMProvider, type LLMRequest, type LLMResponse } from "../src/llm/gateway";

class FakeProvider implements LLMProvider {
  readonly id = "fake";
  constructor(
    private readonly supportedModels: string[],
    private readonly behavior: (req: LLMRequest) => Promise<LLMResponse>,
  ) {}
  supports(model: string): boolean {
    return this.supportedModels.includes(model);
  }
  async complete(req: LLMRequest): Promise<LLMResponse> {
    return this.behavior(req);
  }
}

describe("LLMGateway", () => {
  it("routes to a supporting provider", async () => {
    const gw = new LLMGateway();
    gw.register(new FakeProvider(["model-a"], async (req) => ({
      content: "hi",
      modelUsed: req.model,
      inputTokens: 10,
      outputTokens: 5,
      estimatedCostMillicredits: 1,
      fallbacksAttempted: [],
    })));

    const res = await gw.complete({
      messages: [{ role: "user", content: "hi" }],
      model: "model-a",
      maxTokens: 100,
    });
    expect(res.modelUsed).toBe("model-a");
    expect(res.content).toBe("hi");
  });

  it("falls back through the chain when primary throws", async () => {
    const gw = new LLMGateway();
    gw.register(new FakeProvider(["good"], async (req) => ({
      content: "ok",
      modelUsed: req.model,
      inputTokens: 1,
      outputTokens: 1,
      estimatedCostMillicredits: 1,
      fallbacksAttempted: [],
    })));
    gw.register(new FakeProvider(["broken"], async () => {
      throw new Error("simulated outage");
    }));

    const res = await gw.complete({
      messages: [{ role: "user", content: "x" }],
      model: "broken",
      maxTokens: 100,
      fallbackChain: ["good"],
    });
    expect(res.modelUsed).toBe("good");
    expect(res.fallbacksAttempted).toEqual(["broken(simulated outage)"]);
  });

  it("throws when no provider supports any model in the chain", async () => {
    const gw = new LLMGateway();
    await expect(
      gw.complete({
        messages: [{ role: "user", content: "x" }],
        model: "unknown",
        maxTokens: 10,
      }),
    ).rejects.toThrow(/All models in chain failed/);
  });
});
