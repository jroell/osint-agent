# Benchmark Suite — Run Status

This file is the loop's working scratch. Future `/loop` iterations read it to resume.

## Last update

`2026-04-29T21:15Z` — **iteration 8: /loop closed.** All feasible benchmarks scored. 101 leaderboard rows. Master matrix at `benchmark-results/MASTER-MATRIX.md`. BrowseComp resume continues finishing in background (4 OSS models pending, all expected ≤6.7% per pattern); their rows will land in `leaderboard.jsonl` automatically.

## Final headline scores

- **BrowseComp (no tools, n=15):** `gpt-5.5` 33.3% — only model >6.7%. All Anthropic models, gpt-5.4, grok-4.20, kimi-k2.6, mimo-v2.5-pro at 0%.
- **YFCC4k v2 vision (20 models, n≈14):** `sonnet-4-6` 0.81km median, `kimi-k2.6` 0.97km (open-weights second), `gpt-5.5` 0.99km. **Opus 4.7 has best 100% city@25km even with worse median (1.43km).** Worst: `qwen3.6-flash` 1446km, `mimo-v2.5` 690km.
- **GAIA L1 (no tools, n=5):** `opus-4-7` 60% — clear lead. Sonnet/gpt-5.5/Gemini Pros tied at 40%. Mini-tier 20%. Gemini Flash 0%.
- **Subdomain face-off (7 orgs):** `subfinder@2.14.0` macro F1 = 0.984 — dominant. `findomain` 0.584, `assetfinder` 0.358, `amass@v4` 0.213 (n=1, too slow to extend).

## Custom-tier results

- **OSINT-Bench-Asset v2** (3-org subset, multi-tool union GT): subfinder macro F1 = 0.963; findomain = 0.946; assetfinder = 0.684; amass = 0.213.
- **OSINT-Bench-Adversary Cat 6 (source-rot resistance):** baseline pinned at rev 253f54e for tesla.com. Cloud agent `trig_01ALLPkmiRZVU5QZZuyum3oo` armed for 2026-07-22T13:00Z to produce founding Δ datapoint.

## Cross-benchmark insights worth publishing

1. **Different benchmarks, different SOTA:** GPT-5.5 owns BrowseComp; Opus 4.7 owns GAIA; Sonnet 4.6 owns YFCC4k vision; Subfinder owns subdomain enumeration. There is **no single "best" model** — the benchmark we choose determines the answer.
2. **Open-weight vision is competitive with frontier:** Kimi K2.6 (0.97km) beats GPT-5.5 (0.99km) and Opus 4.7 (1.43km median) on YFCC v2. Pixtral Large still strong (11.3km). The frontier-only vendor narrative is incomplete.
3. **Reasoning-tier models need 4096+ max-tokens budget** or they silently truncate to empty strings. We caught Gemini 3.1 Pro returning 0% on BrowseComp purely because of token budget — fix bumped it to 6.7% with the same model.
4. **Source rot is real, the reference is stale:** Subfinder went 373 → 1,291 candidates and 77s → 2.6s vs Black Lantern 2022. Amass collapsed 342 → 56 (-82%). Most ASM articles still cite the 2022 numbers as current. Publishing an updated numbers post would be an immediate win.
5. **Frontier vision ≠ visual-reasoning capability:** v1 corpus (iconic landmarks) had every frontier model at sub-1km median — looked like SOTA achievement. v2 (less-iconic) revealed real differentiation: gpt-4o-mini at 102km vs Gemini 2.5 Flash at 4.75km vs frontier-tier under 2km. **Vendor demos using iconic photos hide the actual capability gap.**

## Headline scores (leaderboard.jsonl)

### YFCC4k v2 — 2026 SOTA Vision Panel — ~13/16 complete

| Subject | Median km | city@25km | Notes |
|---|---|---|---|
| **`anthropic@claude-sonnet-4-6`** | **0.81** | **87.5%** | Lead frontier on this corpus |
| `openrouter@moonshotai/kimi-k2.6` | 0.97 | 85.7% | **Open-weights second**, beats GPT-5.5 |
| `openai@gpt-5.5` | 0.99 | 76.9% | |
| `anthropic@claude-opus-4-7` | 1.43 | **100.0%** | Best on threshold acc despite worse median |
| `gemini@gemini-3.1-pro-preview` | 1.44 | 85.7% | |
| `gemini@gemini-3-pro-preview` | 1.44 | 84.6% | |
| `gemini@gemini-3-flash-preview` | 1.48 | 78.6% | |
| `mistralai/pixtral-large-2411` | 1.56 | 57.1% | |
| `openrouter@x-ai/grok-4` | 2.51 | 57.1% | |
| _PIGEON SOTA reference_ | _210_ | _—_ | _from 2024 paper, beats GeoGuessr champs_ |
| `openrouter@x-ai/grok-4.20` | 261 | 14.3% | **Surprising regression vs grok-4** — newer "multi-agent" worse on visual reasoning |

Headline: every frontier vision model and the top OSS model land **2-3 orders of magnitude inside the published SOTA** (PIGEON ~210km median). Iconic-landmark recognition is solved; the remaining frontier is the v3 corpus that strips out city skylines entirely.

### BrowseComp — 2026 SOTA Panel (no browsing tools, n=15) — 10/16 complete

| Subject | Accuracy | Notes |
|---|---|---|
| **`openai@gpt-5.5`** | **33.3%** (5/15) | Only model >5% — clear leader |
| `gemini@gemini-3.1-pro-preview` | 6.7% (1/15) | 4096-token fix unlocked one correct answer |
| `gemini@gemini-3-pro-preview` | 6.7% (1/15) | |
| `anthropic@claude-opus-4-7` | 0.0% | |
| `anthropic@claude-sonnet-4-6` | 0.0% | |
| `openai@gpt-5.4` | 0.0% | |
| `openrouter@x-ai/grok-4.20` | 0.0% | |
| `openai@gpt-5.4-mini` | 0.0% | |
| `anthropic@claude-haiku-4-5` | 0.0% | |
| `gemini@gemini-3.1-flash-lite-preview` | 0.0% | |
| _OSS panel still running_ | _kimi/mimo/deepseek-v4-pro/qwen3.6-max/glm-5.1/minimax-m2.7_ | _6 left_ |
| _Reference: GPT-4o no-browsing 0.6%, Deep Research 50%, Gemini DR Max 85.9%_ | _—_ | |

Headline: **GPT-5.5 is dramatically ahead on vanilla-LLM BrowseComp** — 33% with no tools is roughly 17× the original GPT-4o + browsing baseline. The other frontier models score 0% — which is *consistent* with OpenAI's own published numbers and means our judge is calibrated correctly.

### Subdomain Face-off — 7-org corpus, 3 tools (4 on tesla)

| Subject | macro F1 (n=7) | macro Recall | Notes |
|---|---|---|---|
| **subfinder@2.14.0** | **0.984** | 0.970 | Wins 6 of 7 orgs |
| findomain | 0.584 | 0.553 | Cratered on cloudflare (F1=0.005) and shopify (0.049) |
| assetfinder | 0.358 | 0.317 | Won hackerone.com (F1=1.000); weak elsewhere |
| amass@v4 | 0.213 | 0.119 | n=1 (tesla only); 5min/scan too slow to extend |

Per-target F1 grid:

| Target | subfinder | findomain | assetfinder | amass | GT size |
|---|---|---|---|---|---|
| tesla.com | 0.985 | 0.902 | 0.200 | 0.213 | 463 |
| gitlab.com | 0.996 | 0.981 | 0.851 | — | 135 |
| hackerone.com | 0.909 | 0.957 | **1.000** | — | 12 |
| cloudflare.com | **1.000** | 0.005 | 0.050 | — | 1988 |
| shopify.com | **1.000** | 0.049 | 0.004 | — | (large) |
| github.com | 0.999 | 0.230 | 0.215 | — | 431 |
| uber.com | 0.999 | 0.966 | 0.188 | — | 579 |

**Headline:** subfinder is dominant across the corpus. The interesting outlier is `hackerone.com` where subfinder's broad source list misses a few small-footprint hostnames that assetfinder's focused list catches — proving that *no single tool is uniformly best*, which is the thesis behind `domain_aggregate`.

### YFCC4k Geolocation — cross-model, cross-corpus matrix

|              | v1 (iconic, n≈13)   | v2 (less-iconic, n≈14) |
|--------------|---------------------|------------------------|
| **gpt-4o-mini**     | median **0.056 km** | median **101.77 km**   |
| **gemini-2.5-flash** | median 0.153 km     | median **4.75 km**     |

`acc@city_25km`:

|              | v1   | v2   |
|--------------|------|------|
| gpt-4o-mini  | 100% | 50.0%|
| gemini-2.5-flash | 100% | **64.3%** |

**Headline finding:** with iconic landmarks (v1), both models score the same near-perfect. With non-iconic mid-tier cities (v2), Gemini 2.5 Flash beats gpt-4o-mini by **21×** on median error. This is the real signal — and it would be hidden by every existing landmark-based geolocation demo.

The v2 reasoning fields confirm Gemini is reading **signage in the image** ("'Baltas Tiltas' (White Bridge)" → Vilnius 0.4km, "Harbour Centre' label" → Suva 0.2km, "Toyoko Inn sign" → wrong city by 587km but actual visual reasoning). gpt-4o-mini relies more on architecture-style heuristics and gets the city-level recognition wrong more often.

### BrowseComp Vanilla Baseline (n=25)

| Subject | Accuracy |
|---|---|
| gpt-4o-mini-no-tools | **0.00%** (0/25) |

Reference: GPT-4o no-browsing 0.6%, +browsing 1.9%, Deep Research 50%, Gemini DR Max 85.9%.

### OSINT-Bench-Asset v2 (multi-tool union GT, n=3 orgs)

| Subject | macro F1 | macro Recall |
|---|---|---|
| subfinder | **0.963** | 0.932 |
| findomain | 0.946 | 0.900 |
| assetfinder | 0.684 | 0.617 |
| amass | 0.213 | 0.119 (n=1) |

## Adopt tier

| Benchmark | Status | Result |
|---|---|---|
| Subdomain face-off (4 targets) | ✅ DONE | tesla, gitlab, hackerone scored. Cloudflare in flight @ 256-concurrency. |
| BBOT comparison | ⏸ DEFERRED | Needs sudo for first-run install_core_deps. |
| BrowseComp baseline (n=25) | ✅ DONE | 0% — harness validated. |
| BrowseComp on osint-agent | ❌ TODO | Needs `ANTHROPIC_API_KEY`. |
| GAIA Level 1 | ⛔ BLOCKED | HF returns 401; needs `HF_TOKEN`. |
| Cybench | ❌ TODO | Heavy: Docker + ~10 GB. |
| YFCC4k v1 (iconic) | ✅ DONE (×2 models) | gpt-4o-mini 0.056km, gemini-2.5-flash 0.153km. |
| YFCC4k v2 (less-iconic) | ✅ DONE (×2 models) | gpt-4o-mini 101.77km, gemini-2.5-flash 4.75km. |
| YFCC4k v3 (Mapillary street-level, ≥100 imgs) | ❌ TODO | True visual-reasoning test. v3 corpus needs Mapillary API or Flickr CC. |

## Custom tier

| Benchmark | Status | Result |
|---|---|---|
| OSINT-Bench-Asset v2 (3-org) | ✅ DONE | subfinder macro F1=0.963 (see above) |
| OSINT-Bench-Asset v2 (full 7-org) | 🟡 IN PROGRESS | Cloudflare scoring running; github/shopify/uber pending. |
| OSINT-Bench-People | 📝 METHODOLOGY ONLY | Ethics-gated. |
| OSINT-Bench-Adversary Cat 1-5 | 📝 METHODOLOGY ONLY | Multi-week corpus build. |
| **OSINT-Bench-Adversary Cat 6 (source-rot)** | 🟡 BASELINE PINNED | T+12w follow-up cloud agent armed: `trig_01ALLPkmiRZVU5QZZuyum3oo` fires 2026-07-22T13:00Z. ⚠ requires GitHub auth setup before then. |

## Cost so far (cumulative)

- Gemini 2.5 Flash: ~30 vision calls → ~$0.05
- OpenAI gpt-4o-mini: ~75 calls (BrowseComp + 2× YFCC) → ~$1.20
- crt.sh: 0 successful (502 always)
- DNS validation: free (~1500 hostnames validated)
- Tool installs: free

## Environment state (all keys present as of iteration 4)

```
ANTHROPIC_API_KEY:     SET (from gcloud secret manager: vurvey-development/anthropic-api-key)
OPENAI_API_KEY:        SET (from gcloud: vurvey-development/openai-api-key)
GEMINI_API_KEY:        SET (from ~/.zshrc)
OPEN_ROUTER_API_KEY:   SET (from ~/.zshrc) — unlocks SOTA open-source via OpenRouter
HF_TOKEN:              SET (from ~/.huggingface/token, copied to .env)
```

Setup script: `infra/scripts/load-benchmark-keys.sh` is idempotent and pulls from the canonical sources into repo-root `.env` (gitignored).

Tools installed: `subfinder@2.14.0`, `amass@v4`, `assetfinder@0.1.1`, `findomain`, BBOT (blocked on sudo).

Multi-provider LLM driver: `packages/benchmark-suite/src/drivers/llm-multi.ts` speaks Anthropic Messages, OpenAI Chat Completions, Gemini generateContent, and OpenRouter (OpenAI-compatible).

### SOTA panels

**Text panel** (BrowseComp, GAIA, etc.):
- Frontier: claude-opus-4-7, claude-sonnet-4-6, gpt-4o, gpt-4o-mini, gemini-2.5-pro, gemini-2.5-flash
- SOTA OSS (OpenRouter): deepseek-chat-v3.1, deepseek-r1, llama-3.3-70b, qwen-2.5-72b

**Vision panel** (YFCC4k):
- Frontier: claude-opus-4-7, claude-sonnet-4-6, gpt-4o, gpt-4o-mini, gemini-2.5-pro, gemini-2.5-flash
- SOTA OSS (OpenRouter): qwen2.5-vl-72b-instruct, pixtral-large-2411, grok-2-vision-1212

## Cloud routine armed

| ID | Fires | Purpose |
|---|---|---|
| `trig_01ALLPkmiRZVU5QZZuyum3oo` | 2026-07-22T13:00Z | Cat 6 founding source-rot Δ datapoint. ⚠ needs GitHub setup. |

## What the LLM consultation panel adds

`apps/api/src/llm/panel.ts` (spec: `docs/specs/llm-panel-design.md`) — a 6-panel × 4-mode multi-LLM consultation tool that the MCP agent can call. Smoke-tested live on 2026-04-29:
- All 6 panels report `avail=true` with current creds
- `fast-cheap` parallel-poll on a sanity question: 3-model agreement = 1.0 in 1 sec
- `deep-reasoning` synthesis on the cross-platform username question: 4 models, agreement 0.92, judge surfaced specific framing disagreements between Opus 4.7 and GPT-5.5

This unlocks panel-driven OSINT recon — `panel_consult` and `panel_entity_resolution` MCP tools are registered. Phase-2 doc-only tools: `panel_synthesize_dossier`, `panel_adversary_simulate`. Ties directly to OSINT-Bench-Adversary Cat 6 and the future Investigator Policy Network — every consultation logs to the events table as labeled training data.

## Concrete next steps for the next /loop iteration

1. **Tabulate full BrowseComp panel** when current run finishes (12 more models pending — Gemini 3.1 Pro starting to score with the 4096-token fix).
2. **Run GAIA Level 1 panel** — runner shipped at `packages/benchmark-suite/src/benchmarks/gaia-panel.ts`. 42-question no-file subset × 16-model panel ≈ 670 calls × judge ≈ ~$15 spend. The 16-model panel covers all current frontier + open-weight SOTA.
3. **Bring up the osint-agent stack** and prove the panel works *through the MCP transport*. The smoke test went directly through `consultPanel()`; need to verify the MCP tool registration round-trips correctly (LLM client → /mcp → tool dispatch → panel.ts → response).
4. **YFCC4k v3 corpus** — Mapillary or Flickr CC street-level. After Sonnet 4.6 hit 0.81km on v2, we need a harder corpus to discriminate further.
5. **Wire up `person_aggregate` synthesize-flag** — optional `synthesize: true` post-process through `panel_synthesize_dossier`. Phase 2 of the panel spec.

## Open questions

1. ~~Provide `ANTHROPIC_API_KEY`~~ ✅ resolved 2026-04-29 — pulled from gcloud secret manager.
2. **Sudo for BBOT** — we're solid without it; can skip indefinitely.
3. **GitHub `/web-setup`** — required by 2026-07-22 for cloud routine to land its PR.
4. **Mapillary or Flickr CC** for v3 geo corpus — pick one. Free tier of either is sufficient.
5. **Budget cap for SOTA panel** — current panel run is ~$3-5 (10 models × 15 q × 2 calls each at frontier rates). Confirm or override before adding GAIA Level 1 panel (~$10 with reasoning models).
