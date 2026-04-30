import { appendFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { execSync } from "node:child_process";
import type { LeaderboardRow, RunResult } from "./types";

const REPO_ROOT = resolve(import.meta.dir, "../../..");
const DEFAULT_PATH = resolve(REPO_ROOT, "benchmark-results/leaderboard.jsonl");

function gitRev(): string {
  try {
    return execSync("git rev-parse --short HEAD", { cwd: REPO_ROOT }).toString().trim();
  } catch {
    return "uncommitted";
  }
}

export function appendRow(row: RunResult & { family: string }, path: string = DEFAULT_PATH): void {
  const full: LeaderboardRow = {
    ...row,
    ts: new Date().toISOString(),
    rev: gitRev(),
  };
  mkdirSync(dirname(path), { recursive: true });
  appendFileSync(path, `${JSON.stringify(full)}\n`);
}
