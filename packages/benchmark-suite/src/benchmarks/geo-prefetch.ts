/**
 * Pre-fetches geo-corpus images from Wikipedia REST API summaries.
 *
 * For each entry in the corpus, looks up the Wikipedia page (by `wiki_slug`),
 * extracts the `originalimage.source` URL, downloads the bytes, and saves them
 * to `benchmark-results/datasets/geo-images/<id>.jpg`. Polite 1.5s delay
 * between requests to respect Wikimedia rate limits.
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/geo-prefetch.ts
 */
import { readFileSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { resolve } from "node:path";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const CORPUS_ARG = process.argv[2] ?? "apps/api/test/benchmarks/geo-corpus-v1.json";
const CORPUS = resolve(REPO_ROOT, CORPUS_ARG);
const IMAGES_DIR_ARG = process.argv[3] ?? "benchmark-results/datasets/geo-images";
const IMAGES_DIR = resolve(REPO_ROOT, IMAGES_DIR_ARG);

interface CorpusEntry {
  id: string;
  url?: string;
  wiki_slug?: string;
  lat: number;
  lng: number;
  difficulty: "easy" | "medium" | "hard";
  name_for_postmortem: string;
  local_path?: string;
}

interface Corpus {
  version: string;
  subjects: CorpusEntry[];
}

const UA = "osint-agent-benchmark-suite/0.1 (https://github.com/jroell/osint-agent; jroell@batterii.com)";
const DELAY_MS = 1500;

async function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

async function resolveWikiImage(slug: string): Promise<string> {
  const url = `https://en.wikipedia.org/api/rest_v1/page/summary/${encodeURIComponent(slug)}`;
  const res = await fetch(url, { headers: { "User-Agent": UA, accept: "application/json" } });
  if (!res.ok) throw new Error(`wiki summary ${slug}: ${res.status}`);
  const data = (await res.json()) as { originalimage?: { source: string }; thumbnail?: { source: string } };
  const src = data.originalimage?.source ?? data.thumbnail?.source;
  if (!src) throw new Error(`wiki summary ${slug}: no image`);
  return src;
}

async function downloadTo(url: string, path: string): Promise<void> {
  const res = await fetch(url, { headers: { "User-Agent": UA } });
  if (!res.ok) throw new Error(`fetch ${url}: ${res.status}`);
  const bytes = new Uint8Array(await res.arrayBuffer());
  writeFileSync(path, bytes);
}

async function main() {
  mkdirSync(IMAGES_DIR, { recursive: true });
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;
  let ok = 0;
  for (const entry of corpus.subjects) {
    const dest = resolve(IMAGES_DIR, `${entry.id}.jpg`);
    if (existsSync(dest)) {
      entry.local_path = `${IMAGES_DIR_ARG}/${entry.id}.jpg`;
      ok++;
      console.log(`  ${entry.id}: cached`);
      continue;
    }
    try {
      let src: string | undefined = entry.url;
      if (entry.wiki_slug) {
        src = await resolveWikiImage(entry.wiki_slug);
        await sleep(DELAY_MS);
      }
      if (!src) throw new Error("no source URL");
      await downloadTo(src, dest);
      entry.local_path = `${IMAGES_DIR_ARG}/${entry.id}.jpg`;
      ok++;
      console.log(`  ${entry.id}: ${src.slice(-60)} → ${dest.slice(-50)}`);
      await sleep(DELAY_MS);
    } catch (e) {
      console.log(`  ${entry.id}: FAILED ${(e as Error).message}`);
    }
  }
  writeFileSync(CORPUS, JSON.stringify(corpus, null, 2) + "\n");
  console.log(`\nfetched ${ok}/${corpus.subjects.length}`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
