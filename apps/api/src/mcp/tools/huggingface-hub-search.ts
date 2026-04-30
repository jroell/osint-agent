import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["models_by_author", "models_search", "model_detail", "datasets_by_author", "datasets_search", "user_overview", "paper_detail"]).default("models_by_author"),
  query: z.string().min(1).describe("Author/username for *_by_author/user_overview, search keyword for *_search, model_id (e.g. 'meta-llama/Llama-3.1-8B') for model_detail, arXiv ID (e.g. '2310.06825') for paper_detail"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "huggingface_hub_search",
  description:
    "**HuggingFace Hub AI/ML community ER** — queries the public HuggingFace API (no auth). 7 modes: models_by_author (all models published by an author/org), models_search (full-text), model_detail (full card data — base_model lineage, training datasets, license, languages), datasets_by_author / datasets_search, user_overview (researcher/org profile — numModels/numPapers/numFollowers/isPro), paper_detail (HuggingFace Papers entry by arXiv ID — registered HF user authors = real-identity attribution). Aggregations: top authors, tags, languages, total downloads/likes. Use cases: AI researcher career trace, model lineage tracking (zephyr → mistral fine-tune), org publication mapping (every Llama published under meta-llama), paper-author identity resolution (arXiv preprints often have anonymous-style names; HF Papers requires verified identities). Strong complement to crossref + arxiv + nih_reporter for AI/ML researcher graph.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "huggingface_hub_search", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "huggingface_hub_search failed");
    return res.output;
  },
});
