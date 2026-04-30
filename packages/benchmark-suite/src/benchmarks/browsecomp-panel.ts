/**
 * BrowseComp panel — runs the same N questions across SOTA frontier +
 * SOTA open-source models with no browsing tools, judges with a fixed
 * grader (gpt-4o-mini), emits one leaderboard row per model.
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/browsecomp-panel.ts [N=15]
 *   bun ... 15 "anthropic@claude-opus-4-7,openai@gpt-4o"   # custom panel
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";
import { makeLLM, SOTA_TEXT_PANEL } from "../drivers/llm-multi";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const DATASET = resolve(REPO_ROOT, "benchmark-results/datasets/browsecomp.jsonl");

const N = Number(process.argv[2] ?? "15");
const PANEL = (process.argv[3] ?? "").trim()
  ? process.argv[3]!.split(",").map((s) => s.trim())
  : SOTA_TEXT_PANEL;
type Effort = "minimal" | "low" | "medium" | "high" | undefined;
const REASONING_EFFORT = (process.env.REASONING_EFFORT as Effort) ?? undefined;

const ANSWER_TEMPLATE = `Answer the following question.

{Q}

Your response should be in the following format:
Explanation: {your explanation}
Exact Answer: {your succinct, final answer}
Confidence: {0..100}`;

const JUDGE_TEMPLATE = `Judge whether the [response] to [question] is correct based on the [correct_answer].

[question]: {Q}
[response]: {R}
[correct_answer]: {A}

Reply with exactly one word: yes if the response's final answer matches correct_answer (small numeric tolerance and minor formatting differences OK), otherwise no.`;

interface Q { id: string; question: string; answer: string; topic: string; }

const judge = makeLLM(process.env.BROWSECOMP_JUDGE ?? "openai@gpt-5.4-mini");

async function judgeAnswer(q: Q, response: string): Promise<"yes" | "no"> {
  const out = await judge.call({
    prompt: JUDGE_TEMPLATE.replace("{Q}", q.question).replace("{R}", response).replace("{A}", q.answer),
    temperature: 0,
    maxTokens: 8,
  });
  return out.text.trim().toLowerCase().startsWith("yes") ? "yes" : "no";
}

async function runOne(subject: string, questions: Q[]): Promise<void> {
  const llm = makeLLM(subject);
  console.log(`\n→ ${subject}`);
  let correct = 0;
  let attempted = 0;
  let errors = 0;
  const t0 = performance.now();

  for (const q of questions) {
    attempted++;
    try {
      const out = await llm.call({
        prompt: ANSWER_TEMPLATE.replace("{Q}", q.question),
        temperature: 0,
        // Gemini Pro and reasoning models burn 1.5-3k tokens on internal reasoning
        // before emitting a final answer. 16k headroom for high reasoning_effort.
        maxTokens: 16000,
        reasoningEffort: REASONING_EFFORT,
      });
      const verdict = await judgeAnswer(q, out.text);
      if (verdict === "yes") correct++;
      const finalAnswer = (out.text.match(/Exact Answer:\s*(.+?)(?:\n|$)/i)?.[1] ?? "").slice(0, 50);
      console.log(`  ${q.id} gold="${q.answer.slice(0, 40)}" → "${finalAnswer}" → ${verdict}`);
    } catch (e) {
      errors++;
      console.log(`  ${q.id} ERROR: ${(e as Error).message.slice(0, 100)}`);
    }
  }

  const took = (performance.now() - t0) / 1000;
  const score = correct / Math.max(attempted, 1);
  console.log(`  ${subject} → ${correct}/${attempted} = ${(score * 100).toFixed(1)}%  (${errors} errors, ${took.toFixed(1)}s)`);

  const specSuffix = REASONING_EFFORT ? `-effort-${REASONING_EFFORT}` : "";
  appendRow({
    family: "adopt-browsecomp",
    spec_id: `browsecomp-panel-n${attempted}${specSuffix}`,
    subject,
    raw_output: { attempted, correct, errors, reasoning_effort: REASONING_EFFORT ?? "default" },
    score,
    score_breakdown: { accuracy: score, attempted, correct, errors, n_total_dataset: 1266 },
    took_s: took,
    ok: errors === 0,
  });
}

async function main() {
  const lines = readFileSync(DATASET, "utf8").split("\n").filter((l) => l.trim().length > 0);
  const questions = lines.slice(0, N).map((l) => JSON.parse(l) as Q);
  console.log(`browsecomp-panel: n=${questions.length}, panel size=${PANEL.length}`);
  console.log(`panel: ${PANEL.join(", ")}`);

  for (const subject of PANEL) {
    try {
      await runOne(subject, questions);
    } catch (e) {
      console.log(`✗ ${subject} bailed: ${(e as Error).message.slice(0, 200)}`);
    }
  }

  console.log("\n=== panel summary ===");
  console.log(`reference: GPT-4o no-browsing 0.6% / GPT-4o + browsing 1.9% / Deep Research 50% / Gemini DR Max 85.9%`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
