/**
 * YFCC4k vision panel — runs the same image corpus across SOTA frontier
 * vision models + SOTA open-source vision via OpenRouter, scores median
 * geodetic-error per model, emits one leaderboard row per (model, corpus).
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/yfcc4k-panel.ts            # uses v2 corpus
 *   GEO_CORPUS=apps/api/test/benchmarks/geo-corpus-v1.json bun ...         # use v1
 *   bun ... "anthropic@claude-opus-4-7,openai@gpt-4o,gemini@gemini-2.5-pro"
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { haversineKm } from "../runner";
import { appendRow } from "../leaderboard";
import { makeLLM, SOTA_VISION_PANEL } from "../drivers/llm-multi";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const CORPUS = resolve(REPO_ROOT, process.env.GEO_CORPUS ?? "apps/api/test/benchmarks/geo-corpus-v2.json");

const PANEL = (process.argv[2] ?? "").trim()
  ? process.argv[2]!.split(",").map((s) => s.trim())
  : SOTA_VISION_PANEL;

interface CorpusEntry {
  id: string;
  url?: string;
  wiki_slug?: string;
  local_path?: string;
  lat: number;
  lng: number;
  difficulty: string;
  name_for_postmortem: string;
}
interface Corpus { version: string; subjects: CorpusEntry[]; }

interface Pred { lat: number; lng: number; confidence: number; reasoning: string; }

const PROMPT = `You are a geolocation analyst. Look at this image and estimate the latitude and longitude where the photo was taken. Return ONLY a JSON object with exactly these keys: {"lat": <decimal degrees, -90..90>, "lng": <decimal degrees, -180..180>, "confidence": <0..1>, "reasoning": "<one short sentence>"}. No prose outside the JSON. If you cannot recognize the place by name, infer from visual cues (architecture style, vegetation, signage, license plates, vehicle types) — never refuse.`;

function loadImage(entry: CorpusEntry): { b64: string; mime: string } {
  if (!entry.local_path) throw new Error(`no local_path for ${entry.id}`);
  const buf = readFileSync(resolve(REPO_ROOT, entry.local_path));
  return { b64: buf.toString("base64"), mime: "image/jpeg" };
}

function safeParseJSON(text: string): Pred {
  // Handle responses wrapped in code fences or with extra prose.
  let s = text.trim();
  const fence = s.match(/```(?:json)?\s*\n?([\s\S]*?)\n?```/);
  if (fence) s = fence[1]!;
  const objMatch = s.match(/\{[\s\S]*\}/);
  if (objMatch) s = objMatch[0];
  return JSON.parse(s) as Pred;
}

async function runOne(subject: string, corpus: Corpus): Promise<void> {
  const llm = makeLLM(subject);
  if (!llm.vision) {
    console.log(`✗ ${subject}: not flagged as vision-capable, skipping`);
    return;
  }
  console.log(`\n→ ${subject}`);
  const results: Array<{ entry: CorpusEntry; err_km: number; ok: boolean; pred?: Pred }> = [];
  const t0 = performance.now();

  for (const entry of corpus.subjects) {
    if (!entry.local_path) {
      console.log(`  ${entry.id}: SKIP (no local_path)`);
      continue;
    }
    try {
      const { b64, mime } = loadImage(entry);
      const out = await llm.call({ prompt: PROMPT, imageB64: b64, imageMime: mime, jsonOutput: true, temperature: 0, maxTokens: 2048 });
      const pred = safeParseJSON(out.text);
      const err = haversineKm(entry.lat, entry.lng, pred.lat, pred.lng);
      results.push({ entry, err_km: err, ok: true, pred });
      console.log(`  ${entry.id} (${entry.difficulty}) → (${pred.lat.toFixed(2)}, ${pred.lng.toFixed(2)}) err=${err.toFixed(1)}km c=${pred.confidence?.toFixed?.(2) ?? "—"}`);
    } catch (e) {
      results.push({ entry, err_km: 20015, ok: false });
      console.log(`  ${entry.id} FAILED: ${(e as Error).message.slice(0, 120)}`);
    }
  }

  const ok = results.filter((r) => r.ok);
  const errs = ok.map((r) => r.err_km).sort((a, b) => a - b);
  const median = errs.length === 0 ? -1 : errs[Math.floor(errs.length / 2)]!;
  const mean = ok.reduce((a, b) => a + b.err_km, 0) / Math.max(ok.length, 1);
  const acc = (km: number) => ok.filter((r) => r.err_km <= km).length / Math.max(ok.length, 1);

  const breakdown = {
    n_attempted: results.length,
    n_succeeded: ok.length,
    median_km: median,
    mean_km: mean,
    "acc@street_1km": acc(1),
    "acc@city_25km": acc(25),
    "acc@region_200km": acc(200),
    "acc@country_750km": acc(750),
    "acc@continent_2500km": acc(2500),
  };

  console.log(`  ${subject} → median=${median.toFixed(1)}km mean=${mean.toFixed(1)}km city@25km=${(acc(25) * 100).toFixed(1)}%`);
  const score = ok.length === 0 ? 0 : 1 - Math.min(median / 2500, 1);
  appendRow({
    family: "adopt-geo",
    spec_id: `yfcc4k-${corpus.version}`,
    subject,
    raw_output: { results: results.map((r) => ({ id: r.entry.id, err_km: r.err_km, ok: r.ok })) },
    score,
    score_breakdown: breakdown,
    took_s: (performance.now() - t0) / 1000,
    ok: ok.length === results.length,
  });
}

async function main() {
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;
  console.log(`yfcc4k-panel: corpus=${corpus.version}, n=${corpus.subjects.length}, panel size=${PANEL.length}`);
  console.log(`panel: ${PANEL.join(", ")}`);

  for (const subject of PANEL) {
    try {
      await runOne(subject, corpus);
    } catch (e) {
      console.log(`✗ ${subject} bailed: ${(e as Error).message.slice(0, 200)}`);
    }
  }
  console.log("\n=== panel done. Reference SOTA: PIGEON ~210km median on YFCC4k. GeoCLIP ~440km. ===");
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
