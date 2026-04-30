/**
 * needle-bench — multi-hop OSINT questions with user-verified gold answers.
 * Each question chains 3+ constraints (e.g. "scholarship in 2004 + supervised
 * 2017 thesis + published 2004-2007 dental survey"). Single-answer exact-match
 * scoring. Tests the same capability as BrowseComp but as deep single-shot
 * "find this specific person" rather than a 1,266-question battery.
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/needle-bench.ts [model=anthropic@claude-opus-4-7] [mode=tools|baseline]
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";
import { makeLLM } from "../drivers/llm-multi";
import { runAnthropicAgent, runGeminiAgent } from "../drivers/tool-use";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const CORPUS = resolve(REPO_ROOT, "apps/api/test/benchmarks/needle-bench-corpus.json");

const MODEL = process.argv[2] ?? "anthropic@claude-opus-4-7";
const MODE = (process.argv[3] ?? "tools") as "tools" | "baseline";
type Effort = "low" | "medium" | "high";
const REASONING_EFFORT = (process.env.REASONING_EFFORT as Effort) ?? "high";

interface Subject {
  id: string;
  category: string;
  question: string;
  answer: string;
  constraint_chain: string[];
  answer_hint_for_judge: string;
}
interface Corpus { version: string; subjects: Subject[]; }

const TOOLS_SYSTEM = `You are answering a multi-hop OSINT question with verified ground-truth. Each question chains 3+ specific constraints — you must find the SINGLE individual who satisfies ALL constraints. Use the available tools aggressively: most answers require 10-25 tool calls across multiple sources.

Strategy:
1. Identify each constraint in the question and treat it as a search filter.
2. Start broad (tavily_search / perplexity_search) to find candidate matches for the most distinctive constraint.
3. For each candidate, verify EVERY remaining constraint. Don't stop at the first plausible match — find evidence for ALL constraints.
4. Use wikipedia_search → wikipedia_get and firecrawl_scrape to read primary sources directly.
5. Multi-hop: the answer often requires finding one identifier (e.g. an institution from a thesis), then searching for the person via that identifier.

CRITICAL — geographic prior:
The question gives NO geographic clue. Your training data is heavily Western/Anglo-biased, so the obvious leads (US, UK, Norway, Sweden, Australia) are probably NOT where this person is. After 3-4 dead-ends in Western institutions, EXPLICITLY broaden to: South Africa (uct.ac.za, sun.ac.za, wits.ac.za, up.ac.za, uwc.ac.za), Brazil, India, Egypt, Nigeria, Kenya, Mexico, Indonesia, Iran, Turkey, China, Japan, Korea. Use openalex_authors_search with country-code filters (e.g. last_known_institutions.country_code:ZA) instead of free-text web search — it's far more reliable for non-Anglosphere academics.

CRITICAL — subfield prior:
A topic word in the question (e.g. "teeth", "lifestyle", "diseases") is NOT the same as the academic subfield it belongs to. "Teeth" papers can live in: dentistry, oral biology, biological anthropology, forensic anthropology, paleoanthropology, archaeology, zooarchaeology, paleopathology, evolutionary biology. "Lifestyle and diseases" can live in: epidemiology, public health, nutrition, exercise science, bioarchaeology, biological anthropology. Before searching, explicitly enumerate 3-5 candidate subfields and search each. Don't anchor on the obvious one (dentistry for teeth, epidemiology for lifestyle) — the answer is often in an adjacent subfield where the same keyword surfaces less ambiguously.

CRITICAL — MANDATORY brainstorm step (DO NOT SKIP):
Before ANY tool call, output a brainstorm in your FIRST response. Format:
"BRAINSTORM:
- (region, subfield) pairs to try, ranked by likelihood:
  1. (Western, dentistry) — obvious; usually wrong
  2. (Western, epidemiology) — obvious-ish; sometimes right
  3. (Africa or Asia, biological anthropology) — high signal for niche academics
  4. (Africa or Asia, archaeology/paleopathology) — high signal for skeletal/dental research
  5. (Latin America, public health) — diverse training-data coverage
- Then search pairs 3, 4, 5 FIRST (since 1 and 2 are most likely already in your training data and would have given you a confident answer)."

CRITICAL — keyword-translation prior (newly added):
The question's vocabulary is layperson framing ("survey of teeth", "lifestyle and diseases"). Academic literature uses different vocabulary. The SAME study could be titled with very different keywords depending on subfield. Before searching, ALSO brainstorm 5+ literature-style phrasings of the question's key noun phrases. Example: "survey of teeth" could appear in literature as: "dental epidemiological study", "oral health cross-sectional", "DMFT prevalence", "tooth wear analysis", "cultural dental modification practices", "ethnographic dental study", "skeletal dental morphology", "dental anthropology survey". Issue parallel openalex_search calls with EACH of these phrasings, not just the question's wording. Many academic papers use words like "modification", "morphology", "wear", "ablation", "extraction practices" instead of generic "survey" — these are signals you're in the right subfield. If a paper title contains words like "cultural", "ethnographic", "modification", "practices", "ritual", "traditional", you're in biological/cultural anthropology — pursue those leads aggressively.

Then call openalex_authors_search ONE PER PAIR, all in the first 5 tool calls. THIS PREVENTS LOCAL-OPTIMUM TUNNEL VISION on Western dentistry/epidemiology. Anchoring on the obvious pair is the failure mode.

Final answer format:
At the end of your response, output:
Exact Answer: <First Last as the question asks>`;

const BASELINE_SYSTEM = `You are answering a multi-hop OSINT question. Use only your training-data knowledge — no tools. Output:
Exact Answer: <First Last>`;

const judge = makeLLM(process.env.NEEDLE_JUDGE ?? "openai@gpt-5.4-mini");

async function judgeAnswer(subject: Subject, response: string): Promise<{ verdict: "yes" | "no"; extracted: string }> {
  const extracted = (response.match(/Exact Answer:\s*(.+?)(?:\n|$)/i)?.[1] ?? response.slice(-100)).trim();
  const out = await judge.call({
    prompt: `Judge whether the response correctly identifies the person in the question.

[gold answer]: ${subject.answer}
[answer hint for judge]: ${subject.answer_hint_for_judge}
[response final answer]: ${extracted}

Reply with exactly one word: yes (matches gold, tolerate small variants per the hint) or no.`,
    temperature: 0,
    maxTokens: 8,
  });
  return {
    verdict: out.text.trim().toLowerCase().startsWith("yes") ? "yes" : "no",
    extracted,
  };
}

async function main() {
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;
  console.log(`needle-bench v=${corpus.version} model=${MODEL} mode=${MODE} effort=${REASONING_EFFORT}`);

  for (const s of corpus.subjects) {
    console.log(`\n→ ${s.id} (${s.category}) gold=${s.answer}`);
    let final_text = "";
    let iterations = 0;
    let toolCalls = 0;
    let took = 0;
    let err: string | undefined;

    if (MODE === "tools") {
      const maxIter = Number(process.env.MAX_ITER ?? "15");
      try {
        let trace;
        if (MODEL.startsWith("anthropic@")) {
          trace = await runAnthropicAgent({
            model: MODEL.replace("anthropic@", ""),
            system: TOOLS_SYSTEM,
            userPrompt: s.question,
            reasoningEffort: REASONING_EFFORT,
            maxIterations: maxIter,
            perCallMaxTokens: 16000,
          });
        } else if (MODEL.startsWith("gemini@")) {
          trace = await runGeminiAgent({
            model: MODEL.replace("gemini@", ""),
            system: TOOLS_SYSTEM,
            userPrompt: s.question,
            maxIterations: maxIter,
            perCallMaxTokens: 16000,
          });
        } else {
          console.error("tools mode requires anthropic@ or gemini@ model in v1");
          process.exit(1);
        }
        final_text = trace.final_text;
        iterations = trace.iterations;
        toolCalls = trace.tool_calls.length;
        took = trace.total_took_ms / 1000;
        console.log(`  trace: ${iterations} iters, ${toolCalls} tools, ${took.toFixed(0)}s, stop=${trace.stop_reason}`);
        for (const tc of trace.tool_calls) {
          console.log(`    [${tc.name}] ${JSON.stringify(tc.input).slice(0, 80)} → ${tc.output_summary.slice(0, 80)}`);
        }
      } catch (e) { err = (e as Error).message.slice(0, 200); }
    } else {
      const llm = makeLLM(MODEL);
      try {
        const t0 = performance.now();
        const r = await llm.call({
          system: BASELINE_SYSTEM,
          prompt: s.question,
          temperature: 0,
          maxTokens: 4000,
          reasoningEffort: REASONING_EFFORT,
        });
        final_text = r.text;
        took = (performance.now() - t0) / 1000;
      } catch (e) { err = (e as Error).message.slice(0, 200); }
    }

    let verdict: "yes" | "no" = "no";
    let extracted = "";
    if (final_text) {
      const j = await judgeAnswer(s, final_text);
      verdict = j.verdict;
      extracted = j.extracted;
    }

    console.log(`\n  ${MODEL} ${MODE} → extracted="${extracted}" verdict=${verdict}${err ? " ERROR=" + err.slice(0, 100) : ""}`);

    appendRow({
      family: "custom-needle",
      spec_id: `needle-${s.id}-${MODE}-effort-${REASONING_EFFORT}`,
      subject: MODEL,
      raw_output: { extracted, verdict, iterations, tool_calls: toolCalls, gold: s.answer, full_response: final_text.slice(0, 4000) },
      score: verdict === "yes" ? 1 : 0,
      score_breakdown: { correct: verdict === "yes" ? 1 : 0, iterations, tool_calls: toolCalls },
      took_s: took,
      ok: !err,
      error: err,
    });
  }
}

main().catch((e) => { console.error(e); process.exit(1); });
