import type { BenchmarkSpec, Driver, RunResult, ScoringMethod } from "./types";

export async function runOne<I, O>(
  spec: BenchmarkSpec,
  driver: Driver<I, O>,
  scorer: (raw: O, scoring: ScoringMethod) => { score: number; breakdown?: Record<string, number> },
): Promise<RunResult> {
  const start = performance.now();
  try {
    const out = await driver.invoke(spec.input as I, { timeout_s: spec.timeout_s ?? 120 });
    const { score, breakdown } = scorer(out, spec.scoring);
    return {
      spec_id: spec.id,
      subject: driver.subject,
      raw_output: out,
      score,
      score_breakdown: breakdown,
      took_s: (performance.now() - start) / 1000,
      ok: true,
    };
  } catch (e) {
    return {
      spec_id: spec.id,
      subject: driver.subject,
      raw_output: null,
      score: 0,
      took_s: (performance.now() - start) / 1000,
      ok: false,
      error: (e as Error).message,
    };
  }
}

/**
 * Set-based recall/precision/F1 against a ground-truth list.
 * `predicted` and `ground_truth` are case-insensitive matched.
 */
export function setRecallPrecision(
  predicted: string[],
  ground_truth: string[],
): { recall: number; precision: number; f1: number; tp: number; fp: number; fn: number } {
  const gt = new Set(ground_truth.map((s) => s.toLowerCase().trim()));
  const pr = new Set(predicted.map((s) => s.toLowerCase().trim()));
  let tp = 0;
  for (const p of pr) if (gt.has(p)) tp++;
  const fp = pr.size - tp;
  const fn = gt.size - tp;
  const recall = gt.size === 0 ? 1 : tp / gt.size;
  const precision = pr.size === 0 ? 1 : tp / pr.size;
  const f1 = recall + precision === 0 ? 0 : (2 * recall * precision) / (recall + precision);
  return { recall, precision, f1, tp, fp, fn };
}

/** Haversine distance in km between two lat/lng points. */
export function haversineKm(lat1: number, lng1: number, lat2: number, lng2: number): number {
  const R = 6371;
  const toRad = (d: number) => (d * Math.PI) / 180;
  const dLat = toRad(lat2 - lat1);
  const dLng = toRad(lng2 - lng1);
  const a =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(toRad(lat1)) * Math.cos(toRad(lat2)) * Math.sin(dLng / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(a));
}
