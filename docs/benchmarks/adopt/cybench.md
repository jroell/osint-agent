# Cybench (web + forensics subset)

**Family:** `adopt-cybench` · **Reference:** Stanford CRFM, August 2024 (40 CTF tasks across 6 domains)

## What it measures

Capability on professional CTF tasks where the agent has shell access and tool-use. We run only the **web + forensics + misc** subset (~15–20 tasks) — the recon-side of the benchmark — and skip pwn/crypto/rev because those aren't `osint-agent`'s problem domain.

## Why we run it

Cybench is the only cybersecurity benchmark used by both the US AISI and UK AISI in their pre-deployment safety tests of frontier models. Numbers here mean something to security-savvy buyers in a way that pure-research benchmarks don't.

## Methodology

- **Dataset:** `cybench/cybench` GitHub, dockerized challenges. We pin to v1.0.
- **Filter:** Only tasks tagged `web`, `forensics`, or `misc`. Roughly 15 tasks; exact list pinned in `apps/api/test/benchmarks/cybench-recon-subset.txt`.
- **Driver:** `cybench-driver.ts` — boots the per-challenge Docker stack, hands the agent a shell + the MCP tool surface, scores by flag capture.
- **Subtask grading:** Cybench tasks have intermediate subtasks; we record both `flag_captured` (binary) and `subtasks_solved` (count) per challenge.
- **Wall-clock:** 30 minutes per challenge.

## Headline reference scores

Per Cybench paper Table 4, on the full 40-task suite:
- Claude 3.5 Sonnet (no subtask hints): 17.5% complete-task solve rate
- GPT-4o: 12.5%
- o1-preview: 10.0%

The web subset is reportedly easier than the pwn/crypto subset, so a competent agent should clear 30–40% on the recon slice.

## What "winning" looks like

- **Floor:** beat 12.5% (GPT-4o on the full suite, recon subset should be easier).
- **Stretch:** 40% on the recon subset. That would be a publishable result.

## Setup cost

- Cybench needs Docker + ~10 GB of images. CI runs are expensive; we run it locally for now and gate it behind `BENCHMARK_HEAVY=1` in CI.

## Sources

- [Cybench](https://cybench.github.io/)
- [Cybench paper (arXiv 2408.08926)](https://arxiv.org/abs/2408.08926)
- [Stanford CRFM blog](https://crfm.stanford.edu/2024/08/19/cybench.html)
