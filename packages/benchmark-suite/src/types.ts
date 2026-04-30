/**
 * Shared types for the benchmark harness.
 *
 * A BenchmarkSpec describes one comparable evaluation. A RunResult is one
 * invocation of one tool/agent against that spec. A LeaderboardRow is the
 * persisted JSONL form. Scoring is intentionally pluggable: exact-match for
 * tasks with a single right answer, set-based for recall/precision, and
 * LLM-judge for "did the agent justify the finding well enough" tasks
 * (the Trace-Labs-style category).
 */

export type ScoringMethod =
  | { kind: "exact-match" }
  | { kind: "set-recall-precision"; ground_truth: string[] }
  | { kind: "geodetic-error-km"; ground_truth_lat: number; ground_truth_lng: number }
  | { kind: "llm-judge"; rubric: string; max_score: number };

export interface BenchmarkSpec {
  /** Unique slug, e.g. "subdomain-faceoff-tesla", "browsecomp-q0042". */
  id: string;
  /** Family the spec belongs to: "adopt-subdomain", "browsecomp", "osint-bench-asset", etc. */
  family: string;
  /** Human-readable description shown on the leaderboard. */
  description: string;
  /** Free-form task input handed to the runner. */
  input: Record<string, unknown>;
  scoring: ScoringMethod;
  /** Optional max wall-clock seconds for this single spec. */
  timeout_s?: number;
}

export interface RunResult {
  spec_id: string;
  /** Which subject was evaluated: "osint-agent", "subfinder@2.6.5", "amass@4.x", "bbot@2.x", etc. */
  subject: string;
  /** Raw output emitted by the subject — used for downstream analysis. */
  raw_output: unknown;
  /** Numeric score in [0,1] when scoring is bounded; for geodetic-error this is km (lower=better). */
  score: number;
  /** Optional richer score breakdown (recall/precision/F1/etc.) */
  score_breakdown?: Record<string, number>;
  /** Wall-clock seconds for this run. */
  took_s: number;
  /** True if the run completed without throwing; false if timed out / errored. */
  ok: boolean;
  error?: string;
}

export interface LeaderboardRow extends RunResult {
  family: string;
  /** ISO timestamp for when the run completed. */
  ts: string;
  /** Git commit (or "uncommitted") at run time. */
  rev: string;
}

export interface Driver<I = unknown, O = unknown> {
  /** "osint-agent-mcp" | "osint-agent-tool:<name>" | "subfinder-cli" | "bbot-cli" | ... */
  subject: string;
  invoke(input: I, ctx: { timeout_s: number }): Promise<O>;
}
