# BrowseComp

**Family:** `adopt-browsecomp` · **Reference:** OpenAI, April 2025 (1,266 questions)

## What it measures

End-to-end agentic web research: persistent navigation, multi-source synthesis, strategic reasoning. The questions are designed so a single keyword search won't find the answer — the agent must browse multiple pages and combine information.

## Why we run it

It is the closest published benchmark to what `osint-agent` actually does in production: an LLM, holding a single seed (a person, a domain, a phone number), navigating a web of public data to find a hidden fact.

## Methodology

- **Dataset:** [OpenAI BrowseComp](https://github.com/openai/simple-evals) — 1,266 questions, gold answer per question.
- **Subset for adopt-tier:** `browsecomp-50` — first 50 questions seeded from the public set (we record the seed). Full-set runs are paid-tier (`BENCHMARK_PAID=1`).
- **Driver:** `mcp-driver.ts` — drives the running `osint-agent` MCP server with Claude Sonnet as the reasoning model. The agent has access to all registered tools.
- **Scoring:** Exact-match against gold answer (case-insensitive, normalized whitespace). BrowseComp answers are short factual strings, so this is unambiguous.
- **Wall-clock budget:** 5 minutes per question, configurable via `BROWSECOMP_PER_QUESTION_TIMEOUT_S`.

## Headline reference scores

| System | Score |
|---|---|
| GPT-4o (no browsing) | ~0.6% |
| GPT-4o + browsing | ~1.9% |
| OpenAI Deep Research | ~50% |
| Gemini Deep Research | ~59% |
| Gemini Deep Research Max (Apr 2026) | ~85.9% |

## What "winning" looks like

- **Floor:** beat 1.9% (i.e., better than naive GPT-4o + browsing). This is table stakes — if we can't, our tool dispatch is broken.
- **Stretch:** beat 50% on the 50-q subset. That's Deep Research class. Realistic given our tool surface but only with strong agent scaffolding.
- **Aspirational:** beat 60%. Would require non-trivial planner work in the MCP server.

## Cost / privacy notes

- Every BrowseComp question costs ~$0.10–$0.50 in LLM tokens at Sonnet rates depending on tool churn.
- Some questions reference real people; we cache responses per `(spec_id, rev)` so re-runs after code changes only re-run questions that could plausibly differ.

## Sources

- [BrowseComp – OpenAI](https://openai.com/index/browsecomp/)
- [BrowseComp paper (arXiv 2504.12516)](https://arxiv.org/abs/2504.12516)
- [Public leaderboard](https://llm-stats.com/benchmarks/browsecomp)
