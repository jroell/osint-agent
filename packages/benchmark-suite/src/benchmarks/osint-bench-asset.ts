/**
 * OSINT-Bench-Asset v2 — recall+precision of asset discovery using a
 * multi-tool union as ground truth, breaking the v1 self-circularity.
 *
 * For each org in the corpus, finds every `<tool>-<seed_domain>.txt` file in
 * benchmark-results/raw/, DNS-validates each, takes the union as ground truth,
 * and scores each individual tool's set against that union. Reports per-org
 * and macro scores per tool.
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/osint-bench-asset.ts [only_ids]
 *   bun packages/benchmark-suite/src/benchmarks/osint-bench-asset.ts tesla,gitlab,hackerone
 */
import { readFileSync, existsSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { validateHostnames } from "../dns-validate";
import { setRecallPrecision } from "../runner";
import { appendRow } from "../leaderboard";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const RAW_DIR = resolve(REPO_ROOT, "benchmark-results/raw");
const CORPUS = resolve(REPO_ROOT, "apps/api/test/benchmarks/asset-corpus-v1.json");

interface CorpusEntry {
  id: string;
  seed_domain: string;
}
interface Corpus {
  version: string;
  subjects: CorpusEntry[];
}

function parseHostnames(path: string, target: string): string[] {
  const text = readFileSync(path, "utf8");
  const lines = text.split(/\r?\n/);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of lines) {
    const line = raw.trim().toLowerCase();
    if (!line || line.startsWith("#")) continue;
    const noProto = line.replace(/^https?:\/\//, "").split("/")[0]!.split(":")[0]!;
    if (!noProto.endsWith(target)) continue;
    if (seen.has(noProto)) continue;
    seen.add(noProto);
    out.push(noProto);
  }
  return out;
}

function parseGnuTime(path: string): number | undefined {
  if (!existsSync(path)) return undefined;
  const text = readFileSync(path, "utf8");
  const m = text.match(/^real\s+([\d.]+)/m);
  return m ? Number(m[1]) : undefined;
}

function discoverTools(target: string): string[] {
  const tools: string[] = [];
  for (const f of readdirSync(RAW_DIR)) {
    if (!f.endsWith(`-${target}.txt`)) continue;
    if (f.includes(".raw.txt")) continue; // skip the pre-cleaned amass file
    const tool = f.replace(`-${target}.txt`, "");
    if (tool === "crtsh") continue; // crt.sh JSON, not plain hostnames
    tools.push(tool);
  }
  return tools;
}

async function main() {
  const onlyArg = process.argv[2];
  const onlyIds = onlyArg ? new Set(onlyArg.split(",")) : null;
  const corpus = JSON.parse(readFileSync(CORPUS, "utf8")) as Corpus;
  console.log(`OSINT-Bench-Asset v2 (multi-tool union GT) ${onlyIds ? `only=${[...onlyIds].join(",")}` : ""}`);

  // Per-tool macro accumulators
  const macro: Record<string, { f1: number; recall: number; precision: number; n: number }> = {};

  for (const entry of corpus.subjects) {
    if (onlyIds && !onlyIds.has(entry.id)) continue;
    const target = entry.seed_domain;
    const tools = discoverTools(target);
    if (tools.length < 2) {
      console.log(`  ${entry.id} (${target}): SKIP — need ≥2 tool outputs (have ${tools.length})`);
      continue;
    }

    process.stdout.write(`  ${entry.id} (${target}): tools=[${tools.join(",")}] → validating each...\n`);

    const validated: Record<string, string[]> = {};
    for (const tool of tools) {
      const candidates = parseHostnames(resolve(RAW_DIR, `${tool}-${target}.txt`), target);
      const v = await validateHostnames(candidates);
      validated[tool] = v.validated;
      console.log(`    ${tool}: ${candidates.length} candidates → ${v.validated.length} validated`);
    }

    // Union ground truth
    const gt = new Set<string>();
    for (const list of Object.values(validated)) for (const h of list) gt.add(h);
    const ground_truth = [...gt];
    console.log(`    GT (union): ${ground_truth.length}`);

    for (const tool of tools) {
      const score = setRecallPrecision(validated[tool]!, ground_truth);
      const took = parseGnuTime(resolve(RAW_DIR, `${tool}-${target}.time`));
      appendRow({
        family: "osint-bench-asset",
        spec_id: `osint-bench-asset-v2-${entry.id}`,
        subject: tool,
        raw_output: { found: validated[tool]!.length, gt_size: ground_truth.length },
        score: score.f1,
        score_breakdown: {
          recall: score.recall,
          precision: score.precision,
          f1: score.f1,
          tp: score.tp,
          fp: score.fp,
          fn: score.fn,
        },
        took_s: took ?? -1,
        ok: true,
      });
      console.log(
        `    [${tool}] F1=${score.f1.toFixed(3)} recall=${score.recall.toFixed(3)} precision=${score.precision.toFixed(3)} runtime=${took ?? "?"}s`,
      );

      const m = macro[tool] ?? { f1: 0, recall: 0, precision: 0, n: 0 };
      m.f1 += score.f1;
      m.recall += score.recall;
      m.precision += score.precision;
      m.n++;
      macro[tool] = m;
    }
  }

  console.log("\n=== macro per tool ===");
  for (const [tool, m] of Object.entries(macro).sort((a, b) => b[1].f1 / b[1].n - a[1].f1 / a[1].n)) {
    const f1 = m.f1 / m.n;
    const recall = m.recall / m.n;
    const precision = m.precision / m.n;
    console.log(`  ${tool}: macro F1=${f1.toFixed(3)} recall=${recall.toFixed(3)} precision=${precision.toFixed(3)} (n=${m.n})`);
    appendRow({
      family: "osint-bench-asset",
      spec_id: "osint-bench-asset-v2-MACRO",
      subject: tool,
      raw_output: { n: m.n },
      score: f1,
      score_breakdown: { macro_f1: f1, macro_recall: recall, macro_precision: precision, n: m.n },
      took_s: -1,
      ok: true,
    });
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
