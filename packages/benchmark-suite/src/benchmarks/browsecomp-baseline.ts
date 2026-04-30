/**
 * BrowseComp vanilla-LLM baseline — answers questions with NO browsing tools,
 * scores against gold using an LLM judge (mirrors the OpenAI reference eval).
 *
 * Why a vanilla baseline matters:
 *   - It's the floor every browsing-agent must beat. OpenAI reported GPT-4o
 *     no-browsing at ~0.6%; with browsing 1.9%. Establishing our own ~0% floor
 *     proves the harness + judge work end-to-end.
 *   - Once `ANTHROPIC_API_KEY` lands and we wire up the MCP driver, we can
 *     diff against this baseline to attribute the lift to tool use vs raw model.
 *
 * Run:
 *   OPENAI_API_KEY=... bun packages/benchmark-suite/src/benchmarks/browsecomp-baseline.ts [N=25] [model=gpt-4o-mini]
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const DATASET = resolve(REPO_ROOT, "benchmark-results/datasets/browsecomp.jsonl");

const OPENAI_KEY = process.env.OPENAI_API_KEY;
if (!OPENAI_KEY) {
  console.error("OPENAI_API_KEY is required");
  process.exit(1);
}

const N = Number(process.argv[2] ?? "25");
const ANSWER_MODEL = process.argv[3] ?? "gpt-4o-mini";
const JUDGE_MODEL = process.env.BROWSECOMP_JUDGE_MODEL ?? "gpt-4o-mini";

interface Q {
  id: string;
  question: string;
  answer: string;
  topic: string;
}

const ANSWER_PROMPT = `Answer the following question.

{Q}

Your response should be in the following format:
Explanation: {your explanation}
Exact Answer: {your succinct, final answer}
Confidence: {0..100}`;

const JUDGE_PROMPT = `Judge whether the [response] to [question] is correct based on the [correct_answer].

[question]: {Q}
[response]: {R}
[correct_answer]: {A}

Reply with exactly one word: yes if the response's final answer matches correct_answer (small numeric tolerance and minor formatting differences OK), otherwise no.`;

async function callOpenAI(model: string, prompt: string, temp = 0): Promise<string> {
  const res = await fetch("https://api.openai.com/v1/chat/completions", {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${OPENAI_KEY}` },
    body: JSON.stringify({ model, messages: [{ role: "user", content: prompt }], temperature: temp }),
  });
  if (!res.ok) throw new Error(`openai ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as { choices: Array<{ message: { content: string } }> };
  return data.choices[0]!.message.content;
}

function extractFinalAnswer(response: string): string {
  const m = response.match(/Exact Answer:\s*(.+?)(?:\n|$)/i);
  return m ? m[1]!.trim() : response.trim();
}

async function main() {
  const lines = readFileSync(DATASET, "utf8").split("\n").filter((l) => l.trim().length > 0);
  const questions = lines.slice(0, N).map((l) => JSON.parse(l) as Q);
  console.log(`browsecomp-baseline: model=${ANSWER_MODEL}, judge=${JUDGE_MODEL}, n=${questions.length}`);

  let correct = 0;
  let attempted = 0;
  const t0 = performance.now();

  for (const q of questions) {
    attempted++;
    let verdict: "yes" | "no" | "error" = "error";
    let extracted = "";
    try {
      const response = await callOpenAI(ANSWER_MODEL, ANSWER_PROMPT.replace("{Q}", q.question));
      extracted = extractFinalAnswer(response);
      const judge = await callOpenAI(
        JUDGE_MODEL,
        JUDGE_PROMPT.replace("{Q}", q.question).replace("{R}", response).replace("{A}", q.answer),
      );
      verdict = judge.trim().toLowerCase().startsWith("yes") ? "yes" : "no";
      if (verdict === "yes") correct++;
    } catch (e) {
      verdict = "error";
    }
    console.log(
      `  ${q.id} [${q.topic.slice(0, 12).padEnd(12)}] gold="${q.answer.slice(0, 40)}" → "${extracted.slice(0, 40)}" → ${verdict}`,
    );
  }

  const took = (performance.now() - t0) / 1000;
  const score = correct / Math.max(attempted, 1);
  console.log(`\n=== summary ===`);
  console.log(`  attempted: ${attempted}`);
  console.log(`  correct:   ${correct}`);
  console.log(`  accuracy:  ${(score * 100).toFixed(2)}%`);
  console.log(`  took:      ${took.toFixed(1)}s`);
  console.log(`  reference: GPT-4o (no browsing) ~0.6% / GPT-4o + browsing ~1.9% / Deep Research ~50% / Gemini DR Max ~85.9%`);

  appendRow({
    family: "adopt-browsecomp",
    spec_id: `browsecomp-baseline-n${attempted}`,
    subject: `${ANSWER_MODEL}-no-tools`,
    raw_output: { attempted, correct },
    score,
    score_breakdown: {
      accuracy: score,
      attempted,
      correct,
      n_total_dataset: 1266,
    },
    took_s: took,
    ok: true,
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
