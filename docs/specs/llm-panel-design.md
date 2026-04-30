# LLM Consultation Panel — Design

**Status:** v1 in implementation as of 2026-04-29.

**Goal:** Give the MCP-driving agent a way to consult a *team* of frontier + open-weight LLMs for hard OSINT work — multi-hop entity resolution, "connect-the-dots" inference, hypothesis generation, hallucination cross-checking — instead of relying on its own single LLM's opinion. The panel becomes one more tool in the registry, available whenever the operator has the relevant API keys in env.

## Why a panel, not just a fallback chain

The existing `LLMGateway` (`apps/api/src/llm/gateway.ts`) is a fallback chain — try model A, on failure try B. That's *redundancy*, not *expertise diversity*. For hard OSINT work the failure mode isn't "the request 500'd" — it's "one model produced a confident but wrong answer." A panel addresses three real failure modes a fallback chain doesn't:

1. **Training-data diversity.** Anthropic, OpenAI, Google, Alibaba, Moonshot, DeepSeek, xAI all train on *different corpora*. Claude knows GitHub deeply; Qwen and Kimi know Chinese-language platforms (Weibo, Douyin, RED) far better; Grok has live X content. A single-model agent has a single corpus's blind spots — a panel surfaces what any single model misses.
2. **Hallucination triangulation.** If 5 models converge on the same claim it's likely true. If they split 3:2, the claim warrants verification. The disagreement *itself* is signal — a single-model agent has no equivalent.
3. **Adversarial reasoning.** Some models (Grok 4, DeepSeek R1) are noticeably more willing to think adversarially than aligned-tier models. For OSINT-Bench-Adversary work — "if this entity were trying to hide, what would they hide?" — running an "attacker-mindset panel" produces hypotheses an aligned single-LLM won't.

Reference: artificialanalysis.ai 2026-04 Intelligence Index has GPT-5.5 at 60, Opus 4.7 at 57, Gemini 3.1 Pro at 57. These three top models *disagree on factual claims about non-Western entities* about 25% of the time per our spot checks. A panel exploits that disagreement.

## Panel definitions

Six predefined panels. Each is a list of `(provider@model, weight, specialty?)` tuples. Members are auto-filtered by which API keys are present in env at startup — a panel needs at least 2 reachable members to be marked available.

| Panel id | Composition | When the agent picks it |
|---|---|---|
| `deep-reasoning` | `gpt-5.5`, `claude-opus-4-7`, `deepseek-r1`, `kimi-k2.6` | Multi-hop inference, "given findings A+B+C, what entity links them?", hypothesis generation. The panel members are the highest-II reasoners across providers. |
| `broad-knowledge` | `gpt-5.5`, `claude-opus-4-7`, `gemini-3.1-pro-preview`, `grok-4.20`, `kimi-k2.6` | Factual recall about people, orgs, infra. Maximizes training-data diversity (US/EU/CN data; Grok has live X content). |
| `cjk-knowledge` | `qwen3.6-max-preview`, `kimi-k2.6`, `mimo-v2.5-pro`, `claude-opus-4-7` | Investigations involving Chinese / Japanese / Korean entities, platforms, languages. Western-trained models silently fail here — Qwen / Kimi / MiMo were trained at least 30% on CJK corpora and the difference is dramatic on Weibo handles, Douyin profiles, Korean corporate org charts. |
| `vision` | `claude-opus-4-7`, `gpt-5.5`, `gemini-3.1-pro-preview`, `qwen3.6-plus`, `pixtral-large-2411` | Image OSINT: geolocation, license-plate / signage reading, EXIF cross-check, reverse-image lead validation. |
| `fast-cheap` | `claude-haiku-4-5`, `gpt-5.4-mini`, `gemini-3.1-flash-lite-preview` | High-volume routine calls — e.g. classifying every subdomain in a 5,000-row dump. Use when the question is easy but called many times. |
| `adversarial-redteam` | `claude-opus-4-7`, `gpt-5.5`, `deepseek-r1`, `grok-4.20` | "If this entity were trying to hide, what would they hide and where would I look?" Half the panel argues for a hypothesis, the other half tries to demolish it. Maps to OSINT-Bench-Adversary categories. |

Panel composition is in `apps/api/src/llm/panel.ts` and refreshes against artificialanalysis.ai + arena.ai every benchmark cycle (per the `always-verify-current-sota` memory). Open-weight slots specifically rotate quarterly.

## Consultation modes

Four modes. The MCP tool exposes them as a parameter; the agent picks based on how hard the question is.

### `parallel-poll` (cheapest, fastest)

Send the same prompt to every member in parallel. Return all responses + an agreement score derived from a lightweight string-similarity / LLM-judge pass. Fast (~3-5 s), cost ≈ N × per-call.

Best for: factual cross-checks where you want to see disagreement before believing any single model. *"Does the username `pikachu_eu` appear on platform PixelFed?"*

### `synthesis` (default, balanced)

Run `parallel-poll`, then a *judge* (Sonnet 4.6 or gpt-5.4-mini, configurable) reads all member responses and produces:
- `consensus`: synthesized answer
- `agreement_score`: 0..1
- `disagreements`: bullet list of points where members split
- `confidence_warnings`: claims a single model made that nobody else confirmed

Cost ≈ N × per-call + 1 judge call. Best for: most OSINT use cases.

### `adversarial` (expensive, highest signal)

Two-phase:
- Phase 1: prosecution panel ("build the strongest case for hypothesis H").
- Phase 2: defense panel sees Phase 1's output, attacks weakest claims.
- Phase 3: judge synthesizes — which prosecution claims survived? which got demolished?

Cost ≈ 2N × per-call + 1 synthesis. Best for: bug-bounty target validation, "is this finding real or am I about to waste 4 hours?", adversary-aware hypothesis testing.

### `roundtable` (richest, most expensive)

Three rounds of group discussion:
- Round 1: every member proposes independently (no shared context).
- Round 2: every member sees the *other members'* proposals + critiques them.
- Round 3: judge synthesizes — what convergent claims emerged across rounds?

Cost ≈ 3N × per-call + 1 synthesis. Best for: the hardest "connect the dots" cases where you want emergent reasoning, not just polling.

## MCP tool surface

Phase 1 ships two tools. Phase 2 adds two more.

### `panel_consult` (Phase 1 — general-purpose)

```typescript
inputSchema: z.object({
  question: z.string().min(10),
  context: z.string().optional().describe(
    "Raw findings, tool outputs, or evidence. Pasted verbatim into the panel prompt."
  ),
  panel: z.enum(["deep-reasoning", "broad-knowledge", "cjk-knowledge", "vision", "fast-cheap", "adversarial-redteam"]).default("deep-reasoning"),
  mode: z.enum(["parallel-poll", "synthesis", "adversarial", "roundtable"]).default("synthesis"),
  image_b64: z.string().optional(),
  image_mime: z.string().optional(),
})
```

Returns `{ consensus, individual: [{member, response}], agreement_score, disagreements[], cost_millicredits }`.

### `panel_entity_resolution` (Phase 1 — ER specialist)

```typescript
inputSchema: z.object({
  findings: z.array(z.object({
    source_tool: z.string(),
    type: z.enum(["email", "handle", "phone", "name", "domain", "ip", "other"]),
    value: z.string(),
    evidence: z.record(z.unknown()).optional(),
  })).min(2).max(200),
  seed_subject: z.string().optional().describe("Known anchor identity — name/email/handle"),
  panel: z.string().default("deep-reasoning"),
})
```

Designed to be the *natural follow-up* to `person_aggregate` / `domain_aggregate`. Those meta-tools return raw findings — `panel_entity_resolution` reads them and returns clusters.

Returns:
```typescript
{
  clusters: Array<{
    cluster_id: string;
    likely_entity: { name?: string; primary_handle?: string };
    findings: Array<{source_tool, type, value, panel_confidence: 0..1}>;
    agreement_score: number;
    disagreements: string[];
  }>;
  unclustered: Array<finding>;  // findings the panel couldn't link
  manual_review_flags: Array<{finding, reason}>;  // models split — human eye recommended
  cost_millicredits: number;
}
```

### `panel_synthesize_dossier` (Phase 2 — narrative dossier)

Takes raw output from `person_aggregate` and asks the panel to write a Trace-Labs-flag-category dossier: confirmed facts, likely facts, speculative leads, contradictions, follow-up questions.

### `panel_adversary_simulate` (Phase 2 — adversary-aware)

"Given this target's public footprint, what's the entity hiding?" Runs `adversarial` mode with the `adversarial-redteam` panel. Returns hypotheses about typosquats to check, dangling DNS to probe, hidden cross-platform handles. Maps directly to OSINT-Bench-Adversary categories.

## Integration with existing OSINT tools

The panel is purely additive — no changes to existing tools required. But two natural integration points are worth wiring up in Phase 2:

1. **`person_aggregate` post-process flag.** A new optional `synthesize` argument: when `true`, after the meta-tool returns its raw fan-out, automatically pipe results through `panel_synthesize_dossier`. Default `false` to keep base cost low.
2. **`domain_aggregate` adversarial flag.** Same pattern: `audit_adversarial: true` runs `panel_adversary_simulate` over the discovered surface.

Both keep the existing tools untouched — the panel just becomes a richer post-processor when the agent (or operator) opts in.

## Cost model

Each panel call is N model calls. Cost millicredits scale linearly with mode multiplier:

| Mode | Mult | Default-panel ballpark |
|---|---|---|
| parallel-poll | 1× | ~80 millicredits |
| synthesis | 1.2× | ~100 millicredits |
| adversarial | 2.5× | ~250 millicredits |
| roundtable | 3.5× | ~350 millicredits |

The existing credit system pre-deducts `costMillicredits` from the registry definition. We set the registry default to *parallel-poll on default panel* and use the `usage` returned by each provider to true-up post-execution.

## Auto-availability

At server startup, `panel.ts::initPanelRegistry()` checks env:
- `ANTHROPIC_API_KEY` set → claude models available
- `OPENAI_API_KEY` → gpt-5 models available
- `GEMINI_API_KEY` → gemini models available
- `OPENROUTER_API_KEY` (or `OPEN_ROUTER_API_KEY`) → all OpenRouter models available

A panel needs **≥2 reachable members** to be marked available; if a panel falls below quorum, it's still registered but the MCP tool description warns ("requires API keys: …"). The agent calling the tool sees only the available panels; bun-test fails fast on unavailable ones.

## Why this is moat-building, not a feature-add

Tying directly to the proprietary spec (`docs/specs/2026-04-22-osint-agent-design.md`):

- **World Model**: each consultation produces a (question, context, panel, members, individual responses, consensus, agreement) tuple. Logged to the events table. Over time this is a labeled training set for "which panel works best for which question shape" — direct input to the future Investigator Policy Network.
- **Adversary Library**: the `adversarial-redteam` panel's outputs feed directly into the playbook corpus.
- **Predictive Temporal**: tracking which model gets which question right over time is itself a temporal signal — gpt-5.5's correctness on adversary-related questions vs. Opus 4.7's gives us per-model competence drift.

A single-model agent can never produce these signals. The panel is the data-collection mechanism for the future learning-loop layer described in the spec — building it now and logging every consultation is *the* lowest-cost way to start accumulating that corpus.

## What is NOT in v1

- **Per-task specialist routing** — automatic panel selection based on question shape (CJK characters → cjk-knowledge, image attached → vision). v2 will use a tiny classifier; v1 makes the agent pick.
- **Prompt caching** — Anthropic supports it on long contexts; cuts cost ~80% on repeated context. v2.
- **Parallel tool calls within a panel member** — letting each member call OSINT tools mid-thought. That's "agent of agents." Hard, expensive, future.
- **Panel debate transcripts as a return artifact** — only `synthesis`/`disagreements` for v1. Full transcripts in v2.

## v1 implementation files

```
apps/api/src/llm/
  multi-provider.ts          # ports the benchmark-suite multi-provider driver
  panel.ts                   # panel registry + Panel class + 4 modes
  panel-aggregators.ts       # synthesis prompts, agreement scoring

apps/api/src/mcp/tools/
  panel-consult.ts           # MCP tool: panel_consult
  panel-entity-resolution.ts # MCP tool: panel_entity_resolution
```

Registered in `apps/api/src/mcp/tools/registry.ts` after the existing `synthesis.ts` block.
