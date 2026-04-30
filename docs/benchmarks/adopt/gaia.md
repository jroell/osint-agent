# GAIA Level 1

**Family:** `adopt-gaia` · **Reference:** Meta + HuggingFace, 2023 (466 questions, 3 levels)

## What it measures

General-AI-assistant task completion: real-world questions that require reasoning, multimodality, web browsing, and tool use. Humans solve 92%; GPT-4-with-plugins at the original release scored 15%.

## Why Level 1 only (for now)

- Level 1: 146 questions, "should be breakable by very good LLMs"
- Level 2: 245 questions, "strong jump"
- Level 3: 75 questions, "fundamentally challenging"

Level 1 is the right place to first prove the harness end-to-end — it's tractable but non-trivial. Level 2/3 are stretch goals once the agent scaffolding is solid.

## Methodology

- **Dataset:** `gaia-benchmark/GAIA` on HuggingFace, validation split, Level-1 only.
- **Driver:** `mcp-driver.ts` (same path as BrowseComp).
- **Scoring:** GAIA's official scoring rules — exact-match on a normalized answer string. The dataset includes the normalization rules per question.
- **Submission:** GAIA's leaderboard accepts JSONL; the harness emits a leaderboard-compatible file as `benchmark-results/gaia-submission.jsonl` so we can submit if we want.

## Headline reference scores

GAIA's leaderboard is active and updates often. As of April 2026, top systems on Level 1 cluster in the 70–90% range; the floor for "non-trivial agent" is ~30%.

## What "winning" looks like

- **Floor:** beat 30%. If we can't, the MCP server's reasoning loop is the problem, not the tools.
- **Stretch:** ~70%. Competitive with GAIA's top open submissions.

## Sources

- [GAIA dataset – HuggingFace](https://huggingface.co/datasets/gaia-benchmark/GAIA)
- [GAIA leaderboard](https://huggingface.co/spaces/gaia-benchmark/leaderboard)
- [GAIA paper – Meta AI](https://ai.meta.com/research/publications/gaia-a-benchmark-for-general-ai-assistants/)
