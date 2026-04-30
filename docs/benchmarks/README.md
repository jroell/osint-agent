# OSINT-Agent Benchmark Suite

This directory documents the benchmarks `osint-agent` is evaluated on. Two tiers:

- **`adopt/`** — public, off-the-shelf benchmarks. We run them so we can compare against published numbers from OpenAI, Google, Meta, etc.
- **`custom/`** — benchmarks we built because the public ones don't exist. These define the white space we want to win in.

Results land in `benchmark-results/leaderboard.jsonl` (one JSONL row per `(subject, spec_id)` pair). The harness package is at `packages/benchmark-suite/`.

## Why we do this

The Phase-0 spec (`docs/specs/2026-04-22-osint-agent-design.md`) commits us to an "adversary-aware OSINT" position. Without numbers we can't prove we're doing it. The benchmark suite is also the seed corpus for the Phase-2 learning loop — every leaderboard row is also a labeled training example for the Investigator Policy Network.

## Adopt tier (public benchmarks)

| Benchmark | What it measures | Where in `osint-agent` it lights up |
|---|---|---|
| [BrowseComp](./adopt/browsecomp.md) | Hard-to-find entangled facts via web nav | Whole-agent: MCP server + LLM-driven tool dispatch |
| [GAIA Level 1](./adopt/gaia.md) | Real-world reasoning + tool use + browsing | Whole-agent same as above |
| [Subdomain Face-off](./adopt/subdomain-faceoff.md) | DNS recon recall vs Amass/BBOT/etc. | `subfinder_passive`, `domain_aggregate` |
| [Cybench (web/forensics subset)](./adopt/cybench.md) | Real CTF challenges | Recon-side tools + LLM reasoning |
| [YFCC4k geolocation](./adopt/yfcc4k.md) | Image → lat/lng accuracy | `geo_vision`, `reverse_image_search`, `exif_extract_geolocate` |

## Custom tier (our moat)

| Benchmark | What it measures | Why it matters |
|---|---|---|
| [OSINT-Bench-Asset](./custom/osint-bench-asset.md) | Recall+precision of asset discovery against published bug-bounty scopes | Direct comparison vs ASM vendors (CyCognito/Bitsight/Tenable) |
| [OSINT-Bench-People](./custom/osint-bench-people.md) | Trace-Labs-style identity dossier construction with judge validation | The market's first published people-OSINT benchmark |
| [OSINT-Bench-Adversary](./custom/osint-bench-adversary.md) | Detection of actively-hidden infrastructure (typosquats, dangling DNS, leaked secrets, fast-flux) | The differentiator — no commercial vendor benchmarks themselves on this |

## How to run

```sh
# Run a single benchmark family
bun run --filter @osint/benchmark-suite run:subdomain-faceoff

# Run all adopt-tier benchmarks (CI uses this on a weekly cron)
bash docs/benchmarks/run-adopt-tier.sh
```

## Scoring conventions

- **Bounded scores** are in `[0, 1]` (recall, precision, F1, exact-match accuracy). Higher is better.
- **Geodetic error** is in km (lower is better). The leaderboard JSONL leaves the raw value; `score_breakdown` normalizes to `1 - min(km/2500, 1)` so it sorts the same way as the others.
- **Set-based** benchmarks (subdomain, asset) report `{recall, precision, f1, tp, fp, fn}` in `score_breakdown` and use F1 as the headline `score`.
- **LLM-judge** benchmarks (people, adversary) emit a rubric-driven score with the rubric stored alongside the row so it can be replayed under newer judges.

## Reproducibility rules

1. Every row carries the git revision at run time. If you compare two runs, also compare revs.
2. Datasets must be pinned by content hash, not URL — public corpora rotate.
3. No paid APIs in the adopt tier without explicit opt-in via `BENCHMARK_PAID=1` — CI only runs the free path.
4. Each benchmark spec file in `*.bench.ts` is the canonical definition; the methodology doc in `docs/benchmarks/` mirrors it for humans.
