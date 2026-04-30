/**
 * Subdomain Face-Off — adopts the Black Lantern Security 2022 methodology.
 *
 * Reads each tool's raw output file (one hostname per line), DNS-validates,
 * scores set-recall/precision against the union ground-truth, and appends
 * one leaderboard row per subject.
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/subdomain-faceoff.ts
 *
 * Pre-requisite: each tool's output file in benchmark-results/raw/<tool>-<target>.txt
 * (the harness only does the validation + scoring; the scan is the user's
 * responsibility because of right-to-test concerns).
 */
import { readFileSync, existsSync, readdirSync, statSync } from "node:fs";
import { resolve } from "node:path";
import { validateHostnames } from "../dns-validate";
import { setRecallPrecision } from "../runner";
import { appendRow } from "../leaderboard";

const REPO_ROOT = resolve(import.meta.dir, "../../../..");
const RAW_DIR = resolve(REPO_ROOT, "benchmark-results/raw");

interface ToolRun {
  subject: string;
  target: string;
  raw_path: string;
  time_path?: string;
}

function discover(target: string): ToolRun[] {
  if (!existsSync(RAW_DIR)) return [];
  const out: ToolRun[] = [];
  for (const f of readdirSync(RAW_DIR)) {
    if (!f.endsWith(`-${target}.txt`)) continue;
    if (!statSync(resolve(RAW_DIR, f)).isFile()) continue;
    const subject = f.replace(`-${target}.txt`, "");
    const time_path = resolve(RAW_DIR, `${subject}-${target}.time`);
    out.push({
      subject,
      target,
      raw_path: resolve(RAW_DIR, f),
      time_path: existsSync(time_path) ? time_path : undefined,
    });
  }
  return out;
}

function parseHostnames(path: string, target: string): string[] {
  const text = readFileSync(path, "utf8");
  const lines = text.split(/\r?\n/);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of lines) {
    const line = raw.trim().toLowerCase();
    if (!line || line.startsWith("#")) continue;
    // Strip protocol + path if a tool emitted URLs.
    const noProto = line.replace(/^https?:\/\//, "").split("/")[0]!.split(":")[0]!;
    if (!noProto.endsWith(target)) continue;
    if (seen.has(noProto)) continue;
    seen.add(noProto);
    out.push(noProto);
  }
  return out;
}

function parseGnuTime(path: string | undefined): number | undefined {
  if (!path || !existsSync(path)) return undefined;
  const text = readFileSync(path, "utf8");
  const m = text.match(/^real\s+([\d.]+)/m);
  return m ? Number(m[1]) : undefined;
}

async function main() {
  const target = process.argv[2] ?? "tesla.com";
  const runs = discover(target);
  if (runs.length === 0) {
    console.error(`No raw outputs found for target=${target} in ${RAW_DIR}`);
    process.exit(1);
  }

  console.log(`subdomain-faceoff: target=${target}, subjects=${runs.map((r) => r.subject).join(",")}`);

  // Validate every run, build per-subject sets.
  const validated: Record<string, string[]> = {};
  const took: Record<string, number | undefined> = {};
  for (const run of runs) {
    const candidates = parseHostnames(run.raw_path, target);
    process.stdout.write(`  ${run.subject}: ${candidates.length} candidates → validating DNS...`);
    const t0 = performance.now();
    const v = await validateHostnames(candidates);
    const t1 = performance.now();
    validated[run.subject] = v.validated;
    took[run.subject] = parseGnuTime(run.time_path);
    console.log(` ${v.validated.length} validated in ${((t1 - t0) / 1000).toFixed(1)}s (rejected ${v.rejected.length})`);
  }

  // Ground truth = union of all validated sets.
  const gtSet = new Set<string>();
  for (const list of Object.values(validated)) for (const h of list) gtSet.add(h);
  const ground_truth = [...gtSet];
  console.log(`ground_truth (union of validated sets): ${ground_truth.length}`);

  // Score every subject against ground truth.
  for (const run of runs) {
    const found = validated[run.subject]!;
    const score = setRecallPrecision(found, ground_truth);
    appendRow({
      family: "adopt-subdomain",
      spec_id: `subdomain-faceoff-${target}`,
      subject: run.subject,
      raw_output: { found_count: found.length, sample: found.slice(0, 5) },
      score: score.f1,
      score_breakdown: {
        recall: score.recall,
        precision: score.precision,
        f1: score.f1,
        tp: score.tp,
        fp: score.fp,
        fn: score.fn,
        validated_count: found.length,
        ground_truth_size: ground_truth.length,
      },
      took_s: took[run.subject] ?? -1,
      ok: true,
    });
    console.log(
      `  ${run.subject}: validated=${found.length}, F1=${score.f1.toFixed(3)}, recall=${score.recall.toFixed(3)}, runtime=${took[run.subject] ?? "?"}s`,
    );
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
