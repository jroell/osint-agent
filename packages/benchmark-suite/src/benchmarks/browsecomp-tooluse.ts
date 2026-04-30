/**
 * BrowseComp with TOOLS — runs the same questions as browsecomp-panel.ts but
 * with the curated osint-agent tool subset enabled. The headline number is
 * the LIFT vs the no-tools baseline:
 *
 *   "With osint-agent's tool surface, gpt-5.5 jumps from X% → Y%
 *    on BrowseComp."
 *
 * Currently uses runAnthropicAgent; OpenAI/Gemini agent adapters can drop
 * into the same shape.
 *
 * Run:
 *   OPENAI_API_KEY=... ANTHROPIC_API_KEY=... TAVILY_API_KEY=... \
 *     bun packages/benchmark-suite/src/benchmarks/browsecomp-tooluse.ts [N=5] [model=anthropic@claude-opus-4-7]
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";
import { makeLLM } from "../drivers/llm-multi";
import { runAnthropicAgent } from "../drivers/tool-use";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const DATASET = resolve(REPO_ROOT, "benchmark-results/datasets/browsecomp.jsonl");

const N = Number(process.argv[2] ?? "5");
const MODEL = process.argv[3] ?? "anthropic@claude-opus-4-7";
type Effort = "low" | "medium" | "high" | undefined;
const REASONING_EFFORT = (process.env.REASONING_EFFORT as Effort) ?? "high";

interface Q { id: string; question: string; answer: string; topic: string; }

const SYSTEM = `You are answering BrowseComp benchmark questions. Each question requires hard-to-find information that is NOT in your training corpus alone — you MUST use the available tools to research the answer.

Strategy:
1. Start broad with tavily_search or perplexity_search to find candidate sources.
2. Use wikipedia_search → wikipedia_get to verify facts.
3. Use firecrawl_scrape to extract specific facts from a page when needed.
4. Multi-hop: chain multiple tool calls. The answer often requires combining facts from 2-3 sources.

Final answer format: at the end of your response, output:
Exact Answer: <succinct, exactly the form expected — typically a name, year, or short phrase>`;

const JUDGE_TEMPLATE = `Judge whether the [response] correctly answers the [question] given the [correct_answer].

[question]: {Q}
[response]: {R}
[correct_answer]: {A}

Reply with exactly one word: yes if the response's final answer matches (small numeric tolerance and minor formatting differences OK), otherwise no.`;

const judge = makeLLM(process.env.BROWSECOMP_JUDGE ?? "openai@gpt-5.4-mini");

async function judgeAnswer(q: Q, response: string): Promise<"yes" | "no"> {
  const out = await judge.call({
    prompt: JUDGE_TEMPLATE.replace("{Q}", q.question).replace("{R}", response).replace("{A}", q.answer),
    temperature: 0,
    maxTokens: 8,
  });
  return out.text.trim().toLowerCase().startsWith("yes") ? "yes" : "no";
}

async function main() {
  const lines = readFileSync(DATASET, "utf8").split("\n").filter((l) => l.trim().length > 0);
  const questions = lines.slice(0, N).map((l) => JSON.parse(l) as Q);
  console.log(`browsecomp-tooluse: model=${MODEL}, n=${questions.length}, effort=${REASONING_EFFORT}`);
  console.log(`tools: tavily_search, perplexity_search, firecrawl_scrape, wikipedia_search, wikipedia_get`);
  console.log(`reference: vanilla LLM no-tools baselines from earlier — gpt-5.5 33%, opus-4-7 0%, sonnet-4-6 0%, gemini-3.1-pro 6.7%`);

  if (!MODEL.startsWith("anthropic@")) {
    console.error("Phase 1 supports anthropic@ models only. OpenAI/Gemini adapters next.");
    process.exit(1);
  }
  const model = MODEL.replace("anthropic@", "");

  let correct = 0;
  let attempted = 0;
  let errors = 0;
  const t0 = performance.now();
  let totalToolCalls = 0;
  let totalIterations = 0;

  for (const q of questions) {
    attempted++;
    try {
      const trace = await runAnthropicAgent({
        model,
        system: SYSTEM,
        userPrompt: q.question,
        reasoningEffort: REASONING_EFFORT,
        maxIterations: 10,
      });
      totalToolCalls += trace.tool_calls.length;
      totalIterations += trace.iterations;
      const verdict = await judgeAnswer(q, trace.final_text);
      if (verdict === "yes") correct++;
      const finalAnswer = (trace.final_text.match(/Exact Answer:\s*(.+?)(?:\n|$)/i)?.[1] ?? trace.final_text.slice(-80)).slice(0, 60);
      console.log(`  ${q.id} gold="${q.answer.slice(0, 30)}" → "${finalAnswer}" [${trace.iterations} iters, ${trace.tool_calls.length} tools, ${(trace.total_took_ms / 1000).toFixed(0)}s] → ${verdict}`);
    } catch (e) {
      errors++;
      console.log(`  ${q.id} ERROR: ${(e as Error).message.slice(0, 150)}`);
    }
  }

  const took = (performance.now() - t0) / 1000;
  const score = correct / Math.max(attempted, 1);
  console.log(`\n=== summary ===`);
  console.log(`  ${MODEL} effort=${REASONING_EFFORT}: ${correct}/${attempted} = ${(score * 100).toFixed(1)}%`);
  console.log(`  total tool calls: ${totalToolCalls} (avg ${(totalToolCalls / attempted).toFixed(1)} per question)`);
  console.log(`  total iterations: ${totalIterations} (avg ${(totalIterations / attempted).toFixed(1)} per question)`);
  console.log(`  errors: ${errors}, took: ${took.toFixed(1)}s`);

  appendRow({
    family: "adopt-browsecomp",
    spec_id: `browsecomp-tooluse-n${attempted}-effort-${REASONING_EFFORT}`,
    subject: MODEL,
    raw_output: { attempted, correct, errors, total_tool_calls: totalToolCalls, total_iterations: totalIterations, reasoning_effort: REASONING_EFFORT },
    score,
    score_breakdown: {
      accuracy: score,
      attempted,
      correct,
      errors,
      avg_tool_calls_per_q: totalToolCalls / Math.max(attempted, 1),
      avg_iterations_per_q: totalIterations / Math.max(attempted, 1),
    },
    took_s: took,
    ok: errors === 0,
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
