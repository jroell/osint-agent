/**
 * GAIA Level 1 panel — runs the no-file-attachment subset (42 of 53 questions)
 * across SOTA frontier + open-source models. Uses GAIA's official scoring rules
 * (normalized exact-match) via an LLM judge mirror of `browsecomp-panel.ts`.
 *
 * GAIA structure: each row has `task_id`, `Question`, `Level`, `Final answer`,
 * `file_name` (if attached). Level 1 = "should be breakable by very good LLMs".
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/gaia-panel.ts [N=10] [panel?]
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";
import { makeLLM, SOTA_TEXT_PANEL } from "../drivers/llm-multi";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const DATASET = resolve(REPO_ROOT, "benchmark-results/datasets/gaia/level1.jsonl");

const N = Number(process.argv[2] ?? "10");
const PANEL = (process.argv[3] ?? "").trim()
  ? process.argv[3]!.split(",").map((s) => s.trim())
  : SOTA_TEXT_PANEL;
type Effort = "minimal" | "low" | "medium" | "high" | undefined;
const REASONING_EFFORT = (process.env.REASONING_EFFORT as Effort) ?? undefined;

const ANSWER_TEMPLATE = `You are answering a question from the GAIA general-AI-assistant benchmark.

{Q}

Your final answer should match the format implied by the question (number, short phrase, list separated by commas, etc.). Reply in this exact format:

Reasoning: <brief>
Final answer: <succinct, exactly the form expected>`;

const JUDGE_TEMPLATE = `Judge whether the [response] correctly answers the [question] given the [correct_answer].

[question]: {Q}
[response]: {R}
[correct_answer]: {A}

GAIA scoring is exact-match after normalization. Tolerate: case, leading/trailing whitespace, equivalent number formats (e.g. "3" vs "three"), trailing periods. Do NOT tolerate substantive differences.

Reply with exactly one word: yes if the response's final answer matches, otherwise no.`;

interface Q {
  task_id: string;
  Question: string;
  Level: number;
  "Final answer": string;
  file_name?: string;
}

const judge = makeLLM(process.env.GAIA_JUDGE ?? "openai@gpt-5.4-mini");

async function judgeAnswer(q: Q, response: string): Promise<"yes" | "no"> {
  const out = await judge.call({
    prompt: JUDGE_TEMPLATE.replace("{Q}", q.Question).replace("{R}", response).replace("{A}", q["Final answer"]),
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
        prompt: ANSWER_TEMPLATE.replace("{Q}", q.Question),
        temperature: 0,
        maxTokens: 16000,
        reasoningEffort: REASONING_EFFORT,
      });
      const verdict = await judgeAnswer(q, out.text);
      if (verdict === "yes") correct++;
      const finalAnswer = (out.text.match(/Final answer:\s*(.+?)(?:\n|$)/i)?.[1] ?? "").slice(0, 50);
      console.log(`  ${q.task_id.slice(0, 12)} gold="${q["Final answer"].slice(0, 40)}" → "${finalAnswer}" → ${verdict}`);
    } catch (e) {
      errors++;
      console.log(`  ${q.task_id.slice(0, 12)} ERROR: ${(e as Error).message.slice(0, 100)}`);
    }
  }

  const took = (performance.now() - t0) / 1000;
  const score = correct / Math.max(attempted, 1);
  console.log(`  ${subject} → ${correct}/${attempted} = ${(score * 100).toFixed(1)}%  (${errors} errors, ${took.toFixed(1)}s)`);

  const specSuffix = REASONING_EFFORT ? `-effort-${REASONING_EFFORT}` : "";
  appendRow({
    family: "adopt-gaia",
    spec_id: `gaia-l1-no-file-n${attempted}${specSuffix}`,
    subject,
    raw_output: { attempted, correct, errors, reasoning_effort: REASONING_EFFORT ?? "default" },
    score,
    score_breakdown: { accuracy: score, attempted, correct, errors, n_total_dataset: 53 },
    took_s: took,
    ok: errors === 0,
  });
}

async function main() {
  const lines = readFileSync(DATASET, "utf8").split("\n").filter((l) => l.trim().length > 0);
  const all = lines.map((l) => JSON.parse(l) as Q);
  const noFile = all.filter((q) => !q.file_name);
  const questions = noFile.slice(0, N);

  console.log(`gaia-panel: GAIA Level 1, no-file subset (${noFile.length} of ${all.length}), n=${questions.length}, panel size=${PANEL.length}`);
  console.log(`reference: humans 92%, GPT-4-with-plugins ~15%, current SOTA agents 70-90% on the leaderboard`);

  for (const subject of PANEL) {
    try {
      await runOne(subject, questions);
    } catch (e) {
      console.log(`✗ ${subject} bailed: ${(e as Error).message.slice(0, 200)}`);
    }
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
