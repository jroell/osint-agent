/**
 * scrape-evals (osint-agent edition) — faithful smaller-N port of Firecrawl's
 * v2.5 scrape-evals methodology. For each (scraper × URL):
 *   - Coverage = did the scraper return >500 chars of useful content?
 *   - Quality  = LLM-judge rating 0..1 of the extracted content
 *   - Marker   = did the expected_marker appear in the extracted text?
 *
 * Scrapers compared:
 *   - firecrawl  — POST /v1/scrape with main-content extraction
 *   - plain      — fetch() raw HTML (baseline; no JS rendering, no extraction)
 *   - rnet       — JA4-impersonating fetch via py-worker stealth_http_fetch
 *                  (only if py-worker is reachable on http://localhost:8182)
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/scrape-evals.ts [scraper_filter]
 *   bun ... firecrawl,plain
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { appendRow } from "../leaderboard";
import { makeLLM } from "../drivers/llm-multi";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const CORPUS = resolve(REPO_ROOT, "apps/api/test/benchmarks/scrape-evals-corpus-v1.json");

interface CorpusEntry { id: string; url: string; category: string; expected_marker: string; }
interface Corpus { version: string; subjects: CorpusEntry[]; }

interface ScrapeResult {
  ok: boolean;
  text: string;
  bytes: number;
  took_ms: number;
  error?: string;
}

type Scraper = (url: string) => Promise<ScrapeResult>;

const FIRECRAWL_KEY = () => process.env.FIRECRAWL_API_KEY;

/** Strip HTML tags + collapse whitespace. Cheap text-extraction baseline. */
function stripHtml(html: string): string {
  return html
    .replace(/<script[^>]*>[\s\S]*?<\/script>/gi, "")
    .replace(/<style[^>]*>[\s\S]*?<\/style>/gi, "")
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

const SCRAPERS: Record<string, Scraper> = {
  firecrawl: async (url) => {
    const key = FIRECRAWL_KEY(); if (!key) throw new Error("FIRECRAWL_API_KEY not set");
    const t0 = performance.now();
    try {
      const res = await fetch("https://api.firecrawl.dev/v1/scrape", {
        method: "POST",
        headers: { "content-type": "application/json", authorization: `Bearer ${key}` },
        body: JSON.stringify({ url, formats: ["markdown"], onlyMainContent: true }),
        signal: AbortSignal.timeout(45_000),
      });
      const took = performance.now() - t0;
      if (!res.ok) return { ok: false, text: "", bytes: 0, took_ms: took, error: `${res.status}` };
      const data = (await res.json()) as { data?: { markdown?: string } };
      const text = data.data?.markdown ?? "";
      return { ok: text.length > 500, text, bytes: text.length, took_ms: took };
    } catch (e) {
      return { ok: false, text: "", bytes: 0, took_ms: performance.now() - t0, error: (e as Error).message.slice(0, 200) };
    }
  },

  plain: async (url) => {
    const t0 = performance.now();
    try {
      const res = await fetch(url, {
        headers: { "user-agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:124.0) Gecko/20100101 Firefox/124.0" },
        signal: AbortSignal.timeout(30_000),
        redirect: "follow",
      });
      const took = performance.now() - t0;
      if (!res.ok) return { ok: false, text: "", bytes: 0, took_ms: took, error: `${res.status}` };
      const html = await res.text();
      const text = stripHtml(html);
      return { ok: text.length > 500, text, bytes: text.length, took_ms: took };
    } catch (e) {
      return { ok: false, text: "", bytes: 0, took_ms: performance.now() - t0, error: (e as Error).message.slice(0, 200) };
    }
  },

  rnet: async (url) => {
    const t0 = performance.now();
    try {
      const res = await fetch("http://localhost:8182/healthz", { signal: AbortSignal.timeout(2_000) });
      if (!res.ok) throw new Error("py-worker not reachable on :8182");
    } catch (e) {
      return { ok: false, text: "", bytes: 0, took_ms: performance.now() - t0, error: `py-worker unreachable: ${(e as Error).message.slice(0, 100)}` };
    }
    // py-worker tool requires Ed25519-signed POST. Here we'd reproduce signing,
    // but for the benchmark v1 we skip rnet unless the API server is up to proxy.
    return { ok: false, text: "", bytes: 0, took_ms: performance.now() - t0, error: "rnet via py-worker requires signed transport (TODO: proxy via API server)" };
  },
};

const QUALITY_JUDGE_PROMPT = `You are judging the quality of web-scraping output for OSINT use cases.

URL: {URL}
Category: {CATEGORY}
Expected to contain: {MARKER}
Extracted text (first 4000 chars): {TEXT}

Rate the extracted text on a 0..1 quality scale where:
  1.0 = clean, complete main content; ready for an LLM to reason over without further cleanup.
  0.7 = mostly main content but with some nav / ad / boilerplate noise.
  0.4 = main content present but heavily polluted with menus / footers / scripts / repeats.
  0.0 = empty, error page, or pure boilerplate (cookie banners, "Loading...", JS-only shell).

Reply with ONLY a JSON object: {"quality": <0..1>, "reason": "<one short sentence>"}.`;

const judge = makeLLM(process.env.SCRAPE_JUDGE ?? "openai@gpt-5.4-mini");

async function judgeQuality(entry: CorpusEntry, text: string): Promise<{ quality: number; reason: string }> {
  if (!text || text.length < 50) return { quality: 0, reason: "empty or near-empty extraction" };
  const prompt = QUALITY_JUDGE_PROMPT
    .replace("{URL}", entry.url)
    .replace("{CATEGORY}", entry.category)
    .replace("{MARKER}", entry.expected_marker)
    .replace("{TEXT}", text.slice(0, 4000));
  try {
    const out = await judge.call({ prompt, jsonOutput: true, temperature: 0, maxTokens: 256 });
    const parsed = JSON.parse(out.text.trim());
    const q = Math.max(0, Math.min(1, Number(parsed.quality)));
    return { quality: q, reason: String(parsed.reason ?? "").slice(0, 200) };
  } catch (e) {
    return { quality: 0, reason: `judge errored: ${(e as Error).message.slice(0, 100)}` };
  }
}

async function main() {
  const filter = (process.argv[2] ?? "firecrawl,plain").split(",").map((s) => s.trim());
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;

  console.log(`scrape-evals: corpus=${corpus.version}, n=${corpus.subjects.length}, scrapers=${filter.join(",")}`);
  console.log(`reference (Firecrawl v2.5 scrape-evals 1000-URL): firecrawl quality≈0.70 cov≈80%; exa 0.48/74%; zyte 0.47/63%; mid-tier ~0.40-0.45/55-60%; rest/playwright ~0.32/42%`);
  console.log();

  for (const scraperName of filter) {
    const scraper = SCRAPERS[scraperName];
    if (!scraper) { console.log(`  ${scraperName}: SKIP (not implemented)`); continue; }

    console.log(`→ ${scraperName}`);
    let totalCov = 0;
    let totalMarker = 0;
    let totalQuality = 0;
    let qualityCount = 0;
    let totalBytes = 0;
    let totalMs = 0;
    const t0 = performance.now();

    for (const entry of corpus.subjects) {
      const r = await scraper(entry.url);
      const markerFound = r.ok && r.text.toLowerCase().includes(entry.expected_marker.toLowerCase());
      let quality = 0;
      let qReason = "";
      if (r.ok) {
        const j = await judgeQuality(entry, r.text);
        quality = j.quality;
        qReason = j.reason;
        totalQuality += quality;
        qualityCount++;
      }
      if (r.ok) totalCov++;
      if (markerFound) totalMarker++;
      totalBytes += r.bytes;
      totalMs += r.took_ms;
      console.log(
        `  ${entry.id.padEnd(22)} ${entry.category.padEnd(10)} ` +
        `${r.ok ? "✓" : "✗"} ${String(r.bytes).padStart(7)}b ${(r.took_ms / 1000).toFixed(1).padStart(5)}s ` +
        `marker=${markerFound ? "Y" : "N"} q=${quality.toFixed(2)} ${qReason ? `(${qReason.slice(0, 50)})` : (r.error ? `[${r.error.slice(0, 40)}]` : "")}`,
      );
    }

    const n = corpus.subjects.length;
    const cov = totalCov / n;
    const quality = qualityCount > 0 ? totalQuality / qualityCount : 0;
    const markerRate = totalMarker / n;
    console.log(`  ${scraperName} → coverage=${(cov * 100).toFixed(1)}% quality=${quality.toFixed(3)} marker_recall=${(markerRate * 100).toFixed(1)}% avg_bytes=${Math.round(totalBytes / n)} total=${(totalMs / 1000).toFixed(1)}s`);

    appendRow({
      family: "adopt-scrape",
      spec_id: `scrape-evals-${corpus.version}`,
      subject: scraperName,
      raw_output: { n, total_cov: totalCov, total_marker: totalMarker, total_bytes: totalBytes },
      score: quality,
      score_breakdown: {
        coverage: cov,
        quality_mean: quality,
        marker_recall: markerRate,
        n_attempted: n,
        n_covered: totalCov,
        avg_bytes: totalBytes / n,
      },
      took_s: (performance.now() - t0) / 1000,
      ok: true,
    });
  }
}

main().catch((e) => { console.error(e); process.exit(1); });
