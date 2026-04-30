/**
 * YFCC4k-style geolocation benchmark — small smoke-test corpus, Gemini Vision driver.
 *
 * Reads `apps/api/test/benchmarks/geo-corpus-v1.json`, downloads each image,
 * asks Gemini 2.0 Flash to estimate {lat, lng} as JSON, and scores haversine
 * geodetic-error vs ground truth. Prints per-image errors, overall median,
 * and IM2GPS threshold accuracy at street/city/region/country/continent.
 *
 * Run:
 *   GEMINI_API_KEY=... bun packages/benchmark-suite/src/benchmarks/yfcc4k-geo.ts
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { haversineKm } from "../runner";
import { appendRow } from "../leaderboard";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const CORPUS = resolve(REPO_ROOT, process.env.GEO_CORPUS ?? "apps/api/test/benchmarks/geo-corpus-v1.json");

interface CorpusEntry {
  id: string;
  url?: string;
  wiki_slug?: string;
  local_path?: string;
  lat: number;
  lng: number;
  difficulty: "easy" | "medium" | "hard";
  name_for_postmortem: string;
}

interface Corpus {
  version: string;
  subjects: CorpusEntry[];
}

type Provider = "openai" | "gemini";
const PROVIDER: Provider = (process.env.GEO_PROVIDER as Provider) ?? "openai";
const OPENAI_KEY = process.env.OPENAI_API_KEY;
const GEMINI_KEY = process.env.GEMINI_API_KEY;
if (PROVIDER === "openai" && !OPENAI_KEY) {
  console.error("OPENAI_API_KEY required for provider=openai");
  process.exit(1);
}
if (PROVIDER === "gemini" && !GEMINI_KEY) {
  console.error("GEMINI_API_KEY required for provider=gemini");
  process.exit(1);
}

const MODEL =
  process.env.GEO_MODEL ?? (PROVIDER === "openai" ? "gpt-4o-mini" : "gemini-2.0-flash");
const PROMPT = `You are a geolocation analyst. Look at this image and estimate the latitude and longitude where the photo was taken. Return ONLY a JSON object with exactly these keys: {"lat": <decimal degrees, -90..90>, "lng": <decimal degrees, -180..180>, "confidence": <0..1>, "reasoning": "<one short sentence>"}. No prose outside the JSON. If you genuinely cannot guess, return your best guess based on visual cues (architecture style, vegetation, signage, license plates) — never refuse.`;

interface GeminiPrediction {
  lat: number;
  lng: number;
  confidence: number;
  reasoning: string;
}

async function loadImage(entry: CorpusEntry): Promise<{ b64: string; mime: string }> {
  if (entry.local_path) {
    const path = resolve(REPO_ROOT, entry.local_path);
    const buf = readFileSync(path);
    return { b64: buf.toString("base64"), mime: "image/jpeg" };
  }
  if (!entry.url) throw new Error(`no local_path or url for ${entry.id}`);
  const res = await fetch(entry.url, {
    headers: { "User-Agent": "osint-agent-benchmark-suite/0.1 (https://github.com/jroell/osint-agent)" },
  });
  if (!res.ok) throw new Error(`fetch ${entry.url}: ${res.status}`);
  const buf = new Uint8Array(await res.arrayBuffer());
  const mime = res.headers.get("content-type") ?? "image/jpeg";
  return { b64: Buffer.from(buf).toString("base64"), mime };
}

async function geminiLocate(b64: string, mime: string): Promise<GeminiPrediction> {
  const res = await fetch(
    `https://generativelanguage.googleapis.com/v1beta/models/${MODEL}:generateContent?key=${GEMINI_KEY}`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        contents: [
          { role: "user", parts: [{ text: PROMPT }, { inline_data: { mime_type: mime, data: b64 } }] },
        ],
        generationConfig: {
          temperature: 0,
          response_mime_type: "application/json",
          response_schema: {
            type: "object",
            properties: {
              lat: { type: "number" },
              lng: { type: "number" },
              confidence: { type: "number" },
              reasoning: { type: "string" },
            },
            required: ["lat", "lng", "confidence", "reasoning"],
          },
        },
      }),
    },
  );
  if (!res.ok) throw new Error(`gemini ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as { candidates?: Array<{ content: { parts: Array<{ text: string }> } }> };
  const text = data.candidates?.[0]?.content?.parts?.[0]?.text;
  if (!text) throw new Error(`gemini empty response`);
  return JSON.parse(text) as GeminiPrediction;
}

async function openaiLocate(b64: string, mime: string): Promise<GeminiPrediction> {
  const res = await fetch("https://api.openai.com/v1/chat/completions", {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${OPENAI_KEY}` },
    body: JSON.stringify({
      model: MODEL,
      temperature: 0,
      response_format: { type: "json_object" },
      messages: [
        {
          role: "user",
          content: [
            { type: "text", text: PROMPT },
            { type: "image_url", image_url: { url: `data:${mime};base64,${b64}` } },
          ],
        },
      ],
    }),
  });
  if (!res.ok) throw new Error(`openai ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const data = (await res.json()) as { choices: Array<{ message: { content: string } }> };
  const content = data.choices[0]!.message.content;
  return JSON.parse(content) as GeminiPrediction;
}

async function locate(b64: string, mime: string): Promise<GeminiPrediction> {
  return PROVIDER === "openai" ? openaiLocate(b64, mime) : geminiLocate(b64, mime);
}

async function main() {
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;
  console.log(`yfcc4k-geo: model=${MODEL}, n=${corpus.subjects.length}`);

  const results: Array<{ entry: CorpusEntry; pred: GeminiPrediction; err_km: number; took_ms: number; ok: boolean }> = [];

  for (let i = 0; i < corpus.subjects.length; i++) {
    const entry = corpus.subjects[i]!;
    if (i > 0) await new Promise((r) => setTimeout(r, 4000)); // ~15 RPM polite cap
    const t0 = performance.now();
    try {
      const { b64, mime } = await loadImage(entry);
      const pred = await locate(b64, mime);
      const err = haversineKm(entry.lat, entry.lng, pred.lat, pred.lng);
      const took = performance.now() - t0;
      results.push({ entry, pred, err_km: err, took_ms: took, ok: true });
      console.log(
        `  ${entry.id} (${entry.difficulty}) → (${pred.lat.toFixed(2)}, ${pred.lng.toFixed(2)}) err=${err.toFixed(1)}km c=${pred.confidence.toFixed(2)} — ${pred.reasoning.slice(0, 70)}`,
      );
    } catch (e) {
      console.log(`  ${entry.id} FAILED: ${(e as Error).message.slice(0, 200)}`);
      results.push({ entry, pred: { lat: 0, lng: 0, confidence: 0, reasoning: "ERROR" }, err_km: 20015, took_ms: performance.now() - t0, ok: false });
    }
  }

  const ok = results.filter((r) => r.ok);
  const errors = ok.map((r) => r.err_km).sort((a, b) => a - b);
  const median = errors.length === 0 ? -1 : errors[Math.floor(errors.length / 2)]!;

  const thresholdAcc = (km: number) => ok.filter((r) => r.err_km <= km).length / Math.max(ok.length, 1);
  const breakdown = {
    n_attempted: results.length,
    n_succeeded: ok.length,
    median_km: median,
    mean_km: ok.reduce((a, b) => a + b.err_km, 0) / Math.max(ok.length, 1),
    "acc@street_1km": thresholdAcc(1),
    "acc@city_25km": thresholdAcc(25),
    "acc@region_200km": thresholdAcc(200),
    "acc@country_750km": thresholdAcc(750),
    "acc@continent_2500km": thresholdAcc(2500),
  };

  console.log("\n=== summary ===");
  for (const [k, v] of Object.entries(breakdown)) console.log(`  ${k}: ${typeof v === "number" ? v.toFixed(3) : v}`);

  // Per-difficulty breakdown
  for (const diff of ["easy", "medium", "hard"] as const) {
    const subset = ok.filter((r) => r.entry.difficulty === diff);
    if (subset.length === 0) continue;
    const sErrs = subset.map((r) => r.err_km).sort((a, b) => a - b);
    const sMed = sErrs[Math.floor(sErrs.length / 2)]!;
    console.log(`  median@${diff} (n=${subset.length}): ${sMed.toFixed(1)}km`);
  }

  // Score normalization: 1 - min(median_km / 2500, 1) so higher = better, in [0,1].
  // If everything failed (median = -1), score = 0 instead of an artificial 1.0+.
  const score = ok.length === 0 ? 0 : 1 - Math.min(median / 2500, 1);

  appendRow({
    family: "adopt-geo",
    spec_id: `yfcc4k-${corpus.version}`,
    subject: `${PROVIDER}@${MODEL}`,
    raw_output: { results: results.map((r) => ({ id: r.entry.id, err_km: r.err_km, ok: r.ok, conf: r.pred.confidence })) },
    score,
    score_breakdown: breakdown,
    took_s: results.reduce((a, b) => a + b.took_ms, 0) / 1000,
    ok: ok.length === results.length,
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
