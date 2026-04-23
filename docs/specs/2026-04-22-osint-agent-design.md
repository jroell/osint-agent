# OSINT Agent — System Design Spec

**Date:** 2026-04-22 (rev. 4 — solo technical founder + bug-bounty-hunter wedge + OSS-first)
**Author:** Jason Roell
**Status:** Draft — awaiting review
**Codename:** TBD (placeholder: `osint-agent`)

---

## 0. What Changed in Rev. 4 and Why

Rev. 3 was the right strategic spec for a funded team with a sales arm. Rev. 4 is the same architectural thesis reshaped around a **solo technical founder with no sales, no contacts, no people ops — only technical depth, cheap cloud compute, and ship-velocity**. Every structural choice below reflects that constraint.

| Dimension | Rev. 3 | Rev. 4 | Why |
|---|---|---|---|
| Primary buyer | Solo PI / journalist; Compliance mid-market | **Bug bounty hunters + security researchers** (primary), investigative journalists (secondary, unpaid-but-evangelistic) | Only buyer class that self-serves technical tools, lives in channels a solo builder can reach, and evangelizes from technical merit alone. Same OSINT workflow, different label. |
| Go-to-market | Self-serve solo wedge → outbound Compliance | **Pure PLG**: open-source + MCP registries + content + community + conference talks. No outbound, ever. | Solo technical founder cannot run an outbound motion. PLG is the only motion that scales on technical output. |
| Open-source posture | Tool adapters OSS, rest closed | **Aggressive OSS: full MCP server + 80% of tool catalog + agent orchestration glue Apache-2.0. Proprietary: World Model, Adversary Library, Policy Network, federated learning, hosted data plane.** | Open-source is the distribution channel, not a side-effect. Proven pattern: Supabase, Sentry, PostHog, Plausible, Cal.com. |
| Pricing | 4 tiers ($39-$1,499) | **3 self-serve tiers** ($0 / $49 / $199). No Compliance. No Teams in v1. | Every tier buyable by credit card without approval or negotiation. |
| Phase 1 scope | 5 months, 25 tools, 5 ML systems | **Phase 0 (3 mo, 15 tools, zero ML systems) → Phase 1 (4 mo, 35 tools, hypergraph + adversary library v0)** | Ship a credible paid product in 3 months, compound from there. Solo founder timeline reality. |
| ML system sequencing | All 5 started Phase 1 | **World Model Phase 2 (months 7-10). Policy Network Phase 3 (months 13-24).** | One person cannot build five ML systems concurrently. Sequence them behind the MVP that is already making money. |
| Adversary Library curator | Hired expert team | **Jason curates the first 20-30 playbooks himself; community contributes after GA** | You ARE the technical expert. Your expertise is the cold-start. Community + federated discovery grows it. |
| Compliance / SOC 2 | Type I Phase 1.5 | **Defer all compliance certification until an inbound Compliance customer demands it and pays for it** | SOC 2 costs ~$40K and 3-6 months. Only worth it if the buyer is already at the door. |
| Distribution | Content + partnerships | **Explicit, enumerated PLG channel stack (§15)** | Every channel picked for "a solo technical founder can execute it alone without sales skill." |
| Benchmark publication | Phase 1.5 public | **Internal-only for first 6 months; publish in Phase 2 after methodology stabilizes under advisor review** | Published methodology is irreversible. Ship when you can defend it. |
| Team account / SSO / RBAC | Phase 2 GA | **Deferred indefinitely; add only after ≥50 paying users request it** | Unbounded complexity that doesn't serve the wedge buyer. |

The compounding-inference thesis, the moat, the technical architecture, and the 10× positioning all **remain intact**. What changes is the shape of the business and the ordering of the engineering work to match one-person execution.

---

## 1. Executive Summary

**Product:** A recon + OSINT stack shipped as an MCP server + CLI + hosted web app. One integration plugs into Claude Desktop, Cursor, ChatGPT Desktop, or any MCP client; the server exposes ~50 curated OSINT tools backed by a bitemporal knowledge graph with a purpose-built *adversary-aware* connection-finding engine. The moat is a compounding inference layer — World Model, federated pattern learning, predictive temporal reasoning, adversary library, and investigator policy network — that makes the product measurably smarter with every investigation run through it.

**Primary buyer:** Bug bounty hunters and security researchers. These users already self-serve technical tools with personal credit cards ($20-$200/mo budgets, individual bounties $500-$50K), live in distribution channels a solo technical founder can reach (Twitter/X, Discord, GitHub, Hacker News, YouTube, CFPs), and evangelize from technical merit. Their daily workflow — external recon, attack surface discovery, shadow infra mapping, tech fingerprinting, exposed asset detection — is exactly the workflow the product automates.

**Secondary wedge:** Investigative journalists. Unpaid-or-low-paid, but one ProPublica/Bellingcat byline crediting the tool is worth thousands of paid signups. Free Hunter-tier accounts in exchange for attribution.

**Positioning tagline:**
> **"The recon stack that finds what someone is hiding."**
> Adversary-aware OSINT for bounty hunters, security researchers, and journalists. Multi-source recon + probabilistic graph reasoning + a learned library of hidden-infrastructure patterns. One MCP server, your LLM client of choice, $0 free tier.

**10× contract (the testable claim):**
On an internal-then-published benchmark of real-world recon scenarios, the product surfaces ≥ 4× more non-obvious, later-verified connections per case than the best commercial alternative (Maltego CE, SpiderFoot OSS, Shodan, a stock LLM+MCP-OSINT-tools setup) — at lower cost and lower time-to-insight. Compounds quarter-over-quarter by construction.

**Why incumbents can't catch us:**
- Data broker wrappers (Skopenow, IRBsearch, OSINT Industries): no graph, no inference, no learning.
- Pivot tools (Maltego, SpiderFoot): deterministic transforms, no probabilistic reasoning, no cross-case learning.
- Enterprise OSINT (Social Links, ShadowDragon): contractual per-tenant siloing prevents cross-tenant priors.
- Stock LLM + generic MCP tools: no world model, no adversary library, no federated learning — retrieval without the moat.

Every one of them either rearchitects their data plane (3-5 year rewrite) or loses the segment. Greenfield is our structural advantage.

---

## 2. The Core Thesis: Compounding Inference

Every OSINT product in market today treats each investigation as independent. You run queries, pull results, draw lines between them, produce a report. The next investigation starts from zero.

This is wrong in the same way pre-Waymo self-driving was wrong when each car's experience was trapped in that car. Value compounds when every case contributes to a shared understanding of how the world actually looks — and especially how the world looks *when someone is trying to hide things in it*.

The architectural consequence: our core runtime is not a retrieval pipeline. It is an **inference engine** that:

1. Holds **population-scale priors** over what entities, relationships, and adversary behaviors look like, learned from aggregate platform history.
2. Applies those priors on every new query as **Bayesian updates**, not heuristics.
3. **Predicts** what the graph should look like if we had complete information, not just what it currently contains.
4. **Recognizes adversary behavior patterns** (shadow infra, shell structures, sock-puppet networks, identity rotation, document-laundering chains) that no single user has ever seen all of.
5. **Suggests next actions** with the composite judgment of thousands of expert-hour-equivalents (Phase 3).
6. Gets stronger every week without any explicit retraining step from the user's perspective.

The MCP server, tool catalog, graph storage, and retrieval stack are all *supporting infrastructure* for this inference engine. Rev. 2 treated them as the product; rev. 3 and rev. 4 treat them as scaffolding.

---

## 3. Product Shape

### 3.1 Interfaces

| Interface | Ships in | Who |
|---|---|---|
| MCP server (stdio local) | Phase 0 | Any user with a local LLM client (Claude Desktop, Cursor, Cline, Zed AI, etc.) |
| MCP server (Streamable HTTP, remote) | Phase 0 | Hosted LLM clients + power users |
| CLI (`osint`) | Phase 1 | Technical users who live in the terminal (the core buyer) |
| REST API | Phase 1 | Scripting / automation / Burp + Caido plugins |
| Web UI (SvelteKit, minimal) | Phase 2 | Users who want a dedicated workbench + dashboards |
| Burp Suite + Caido plugins | Phase 2 | In-workflow integration for bounty hunters |

### 3.2 Deployment model

Thin local MCP client authenticates against the hosted backend. All expensive / cache-worthy / learning-fed operations run centrally. Central hosting is non-negotiable — federated learning and population-scale priors require centralization. BYOK available on the Operator tier for LLM provider only; the inference engine is never BYOK, because it is the product.

Early operational simplification: **single-tenant-per-VM until volume demands multi-tenancy**. Each paying user gets a small Fly.io machine (~$3-10/mo compute) with a FalkorDB + Graphiti instance and a shared Postgres + DragonflyDB. Multi-tenant consolidation comes in Phase 2 when the user count justifies the engineering cost.

### 3.3 Pricing (3 tiers, all self-serve)

| Tier | Price | Included | Who it's for |
|---|---|---|---|
| **Free** | $0 | 100 credits/mo · open-source tools only · 7-day retention · single-seat | Evaluation, journalists (with attribution), OSS users, data contributors to the world model |
| **Hunter** | $49/mo | 5,000 credits · all tools · hosted inference · stealth scraping (JA4+) · 30-day retention · webhooks | The core tier — bug bounty hunters, security researchers, journalists on paid accounts |
| **Operator** | $199/mo | 25,000 credits · priority queue · Adversary Library · Predictive Temporal · API access · 180-day retention · Burp/Caido plugins · BYOK LLM | Top-percentile bounty hunters, red-teamers-of-one, established journalists, independent researchers |

Credits unit: **1 credit = $0.01 retail** (marked up ~3× over underlying cost). Credit-pack top-ups $10-$500 available. Overage at 1.2× retail. No tier requires sales contact, contract negotiation, or approval workflow. Stripe Checkout to dashboard in < 60 seconds.

**Deferred tiers (add only if market pulls):**
- Compliance / Due Diligence ($1,500+/mo) — add when first inbound compliance customer arrives; defer SOC 2 until they commit.
- Team (multi-seat + shared cases) — add when ≥ 50 paying users request it.
- Agency / reseller — add if three or more asks.

### 3.4 Compliance posture

- **Acceptable Use Policy (AUP)** enforced programmatically (anomaly detection on usage patterns, red-flag query review, account suspension for stalking/doxing/harassment).
- **No KYC beyond verified email + payment method** for Free and Hunter tiers. Light KYC attestation added for Operator.
- **GDPR + CCPA DSAR flow** built in from Phase 1 (users can export / delete their case data within 30 days).
- **OFAC screening** on sign-up (reject sanctioned-jurisdiction customers automatically).
- **Federated learning privacy guarantees published** — differential privacy ε budgets, data flow diagrams, what leaves the tenant boundary. Living document.
- **SOC 2 and ISO 27001 explicitly deferred** until an inbound paying customer demands certification and is willing to fund the ~$40K cost.

---

## 4. The Compounding Inference Architecture

Six systems. Same as rev. 3. **Sequenced for solo-founder execution**: hypergraph + distributional edges in Phase 1, the rest arrive in their simplest-possible form between Phase 2 and Phase 3.

### 4.1 World Model

**What it is:** a learned probabilistic model over the distributions of entities, relationships, and behaviors observed across platform history. Answers questions like "given this address's topological signature, what's P(mail-drop | observation)?"

**Ships:** Phase 2 v0.5 (structural layer only — GraphSAGE inductive embeddings, ~15 role categories across top 5 entity types, cold-started from ~15K labeled examples curated by Jason from public data: OFAC designations, FinCEN shell indicators, known typosquat domain sets, published bounty-hunter writeups, academic datasets). Phase 3 v1.0 adds behavioral layer (transformer over temporal event sequences). Cold-start data realism has been right-sized — 15K is achievable solo; 100K was not.

**Why solo-feasible:** GraphSAGE training on 15K labeled examples fits a single A10G GPU overnight on Fly.io GPU or on a cheap spot instance. Continuous training is the Ray cluster job (Phase 2). Incumbents can't copy because they lack cross-tenant longitudinal data.

### 4.2 Federated Pattern Learning

Cross-tenant learning with cryptographic privacy. Same three channels as rev. 3.

**Channel A (always on, no opt-out):** fully aggregated structural statistics with differential privacy (ε ≤ 1.0). Degree distributions, edge-type frequencies, temporal velocities. Feeds World Model's structural layer.

**Channel B (default on, explicit opt-out):** de-identified meta-features from resolved cases. No PII, no tenant IDs, no entity identifiers — only "a path of type [X→Y→Z] with confidence profile [0.9, 0.7, 0.8] was later verified as a true positive." Aggregated via secure aggregation (Flower + homomorphic sum). Feeds Adversary Library discovery and retrieval strategy compilation.

**Channel C (opt-in only):** full case trajectories, k-anonymized at k≥20, pseudonymized, with 15% credit rebate. Feeds Policy Network (Phase 3). Explicit, contractual, documented. *Legal review required before ship — the rebate-for-consent structure needs GDPR compatibility check.*

**Ships:** Phase 2 (Channels A + B with DP activation, pending independent crypto audit). Channel C activation deferred to Phase 3 once legal review clears and trajectory volume justifies.

### 4.3 Predictive Temporal Layer

Continuously forecast graph evolution, surface missing edges, support counterfactual queries.

**Ships:** Phase 2 v1.0 — continuous TGN training per-tenant (memory-efficient 2026-era variants), hourly background missing-link scan, arrival-time distribution on predicted edges. Counterfactual reasoning is a Phase 3 research item — spec says "available" but v1 may be limited; we under-promise publicly until it works.

**Why this matters for bounty hunters:** "the tool found a predicted subdomain that materialized 12 days later with the expected TLS cert" is a Hacker News post waiting to happen. Shadow infrastructure prediction is exactly this buyer's pain.

### 4.4 Adversary Behavior Library

Curated library of adversary playbooks: identity rotation, shell-company construction, sock-puppet topologies, typosquat clusters, subdomain takeover patterns, orphaned-cloud-resource fingerprints, document-laundering chains, beneficial-owner obfuscation.

**Ships:**
- **Phase 1 v0:** 20 hand-curated playbooks, written by Jason, drawing on FinCEN enforcement actions, published bounty writeups, Unit 42 / Mandiant adversary reports, Bellingcat case studies, FATF typologies. Each playbook is a parameterized subgraph template + distinguishing features + reference cases + writeup.
- **Phase 2 v1.0:** 60+ playbooks via combination of Jason curation + community contribution (GitHub PRs to a playbook repo, Apache-2.0 licensed) + automated discovery from Channel B meta-features (clusters of recurring path patterns promoted through human review).
- **Phase 3 v2.0:** 150+ playbooks, continuous discovery pipeline mature.

**Subgraph matching:** approximate match via VF3 algorithm + neural subgraph matcher, run on every material graph write. Matches above threshold flagged to investigator with "this looks like [playbook]; N similar cases historically resolved as [outcome]."

**Why this is the most marketable feature:** Every bounty hunter's mental model for recon is "find what the org is hiding." The Adversary Library operationalizes that directly. Published playbooks become a massive content asset — each one is a blog post + Twitter thread + potential conference talk.

### 4.5 Investigator Policy Network

Next-action model trained on decision trajectories from resolved cases. Imitation learning Phase 3 v1.0; RL fine-tuning Phase 3+ v2.0.

**Ships:** Phase 3 v1.0 (months 13-18). Requires ~10K+ opt-in resolved trajectories (Channel C) — realistic given Phase 2 user growth. Deployed as "second voice" alongside Claude Advisor at Plan/Pivot checkpoints.

**Honest sizing:** spec no longer promises "strictly dominates top investigators" at v1.0. That's a v2.0+ claim with verdict-reward RL fine-tuning and volume > 100K trajectories.

### 4.6 Hypergraph + Probabilistic Data Model

Data model substrate. **Ships in Phase 1** — this is a foundational choice that cannot be retrofitted later without massive migration cost. Everything else in §4 assumes this substrate.

**Hypergraph:** real investigative relationships are N-ary. "A introduced B to C at location D on date E while employed by F" is one semantic fact, not six binary edges. Emulated on FalkorDB via typed "event nodes" with typed participant-edges.

**Distributional edges:** each edge carries a posterior distribution (Bernoulli for existence, Normal/Beta for continuous attributes, Dirichlet for multi-valued priors) rather than a scalar confidence. Path-finding propagates uncertainty properly. Verdicts produce calibrated probabilities with credible intervals.

**Performance commitment:** if p95 query latency on hyperedge-heavy operations exceeds 200ms on tenants ≥ 1M nodes during Phase 1, TypeDB migration accelerates into Phase 2 rather than Phase 3. Measurement-contingent, not hand-wavy.

---

## 5. System Architecture — Layered View

```
User's LLM client (Claude Desktop / Cursor / Cline / ChatGPT)
        │ MCP (stdio or Streamable HTTP)
        ▼
Thin local MCP client (TS) — auth + tool proxy
        │ HTTPS (signed)
        ▼
Edge: Cloudflare (WAF, Turnstile, R2 edge)
        │
        ▼
Product API (Bun + TS + ElysiaJS)
  - Auth / billing (Stripe) / credit metering
  - MCP-over-HTTP + REST + CLI API endpoints
  - LLM Gateway (Anthropic / OpenRouter / BYOK)
        │
   ┌────┴──────┬──────────────┬─────────────────┐
   ▼           ▼              ▼                 ▼
Tool workers  Browser      Python workers    Stealth HTTP
(Go + PD    workers       (GLiNER, Jelly-   (rnet / JA4+)
 libs)      (PW + Nodriver  fish-8B, holehe,
            + Camoufox)     maigret, ghunt)
   │           │              │                 │
   └───────────┴──────────────┴─────────────────┘
                      │
                      ▼
Ingest / Entity Resolution pipeline
  GLiNER-Relex → Jellyfish-8B → Claude Haiku via BAML (tiered)
  Splink probabilistic linkage (per-tenant + federated warm-start)
  libpostal, libphonenumber, tldextract, ens-normalize
                      │
                      ▼
┌──────────────────────────────────────────────────────────┐
│ COMPOUNDING INFERENCE LAYER                              │
│   World Model ◄─ Federated Aggregator (DP) ─► Adversary │
│        ▲              ▲                        Library  │
│        │              │                         ▲       │
│        Predictive  Policy Network ──────────────┘       │
│        Temporal    (Phase 3)                            │
└──────────────────────────────────────────────────────────┘
                      │
                      ▼
Knowledge layer
  FalkorDB (multi-tenant hypergraph, distributional edges)
  Graphiti (bitemporal layer)
  Postgres 16 + pgvector + pgvectorscale (OLTP, evidence, embeddings, trajectory logs)
  Cloudflare R2 (immutable artifacts)
  DragonflyDB (cache + rate-limit + queue)
                      │
                      ▼
Retrieval & Reasoning layer
  Hybrid (BM25 + vector + graph-walk)
  HippoRAG 2 substrate + PathRAG pruning + OG-RAG gating
  World-model-conditioned ranking (Phase 2+)
  ColBERTv2 + Cohere Rerank + RRF
                      │
                      ▼
Agent orchestration
  LangGraph state machine · BAML typed I/O · DSPy-compiled prompts
  Claude Advisor (Opus 4.7 + Haiku 4.5) via LLM Gateway
  Policy Network second voice (Phase 3)
  12 Connection-Finding primitives exposed as MCP tools
```

Observability: OpenTelemetry → Grafana Cloud + Sentry.
Jobs: River (Go) on Postgres.
Training (Phase 2+): Ray cluster on GCP for World Model + TGN. A10G for TGN; H100 rented hourly for rare heavy training runs.

---

## 6. Data Model

### 6.1 Canonical entity types

13 types from rev. 2 + 2 new from rev. 3:

- `Person`, `EmailAddress`, `PhoneNumber`, `PhysicalAddress`, `Domain`, `IPAddress`, `Organization`, `SocialAccount`, `Document`, `Image`, `CryptoWallet`, `Event`, `Claim`
- `Hyperedge` — first-class N-ary relation node (new in rev. 3)
- `Playbook` — adversary library template reference (new in rev. 3)

**Required field on every entity type:** `world_model_scores` — Categorical distribution over role labels. Written at ingest (initially flat/uniform in Phase 1; informed by World Model in Phase 2+). Queryable. First-class.

### 6.2 Edge types

All edge types from rev. 2, plus:
- `PARTICIPATES_IN` (entity → Hyperedge)
- `MATCHES_PLAYBOOK` (entity or Hyperedge → Playbook, with match-confidence distribution)
- `PREDICTED_TO_EXIST` (entity → entity, with arrival-time distribution) — Phase 2+, written by the Predictive Temporal layer

Every edge carries a posterior distribution (not scalar confidence):
```json
{
  "edge_type": "LIVES_AT",
  "from": "person_123", "to": "address_456",
  "posterior": {
    "existence": { "type": "Bernoulli", "p": 0.87, "ci95": [0.72, 0.95] },
    "valid_from": { "type": "Normal", "mu": "2023-04-15", "sigma_days": 30 },
    "valid_to":   { "type": "Normal", "mu": "2025-11-02", "sigma_days": 90 }
  },
  "observed_at": "2026-04-22T14:30:00Z",
  "superseded_at": null,
  "sources": ["claim_789", "claim_790"],
  "method": "federated-match+splink-v3"
}
```

### 6.3 Chain-of-custody Claim model

Every assertion backed by a `Claim`: raw artifact hash (R2 pointer), retrieval timestamp (system-time), tool + version, proxy used, agent reasoning-trace ID, initiating user. Evidence bundle export (Phase 2) produces Merkle-rolled manifest.

### 6.4 Bitemporal semantics

Valid-time + system-time on every node and edge. Augmented in Phase 2+ by `predicted_arrival_time` on edges of type `PREDICTED_TO_EXIST`.

---

## 7. Entity Resolution Pipeline

Three-tier routing (unchanged from rev. 3):

1. **Tier 1 (~80%):** GLiNER + GLiNER-Relex — deterministic encoder-only NER + RE
2. **Tier 2 (~18%):** Jellyfish-8B self-hosted — Llama-3 instruction-tuned for data preprocessing, ~0.08s/instance on A10G
3. **Tier 3 (~2%):** Claude Haiku 4.5 via BAML — ambiguous long-context cases

Pipeline stages:
1. Extract (tiered)
2. Normalize (libpostal, libphonenumber, tldextract, ens-normalize)
3. Candidate-match via Splink blocking rules
4. Fellegi-Sunter probabilistic scoring (per-tenant model warm-started from federated base in Phase 2+)
5. **World-model integration (Phase 2+):** candidate merges consult world model; high-Splink-weight merges with incompatible role-distributions auto-escalate to Tier 3
6. Thresholded merge: auto-merge / review-queue / reject
7. Transactional graph write to FalkorDB + Graphiti with full provenance

**SLAs:**
- p95 ingest latency: < 2s (tenant graph ≤ 10M nodes)
- Throughput: ≥ 50 artifacts/s per worker
- Precision ≥ 0.98; Recall ≥ 0.85
- World Model scoring: < 50ms sync path, < 500ms async path (Phase 2+)

---

## 8. Retrieval & Reasoning

### 8.1 Composite reasoning (HippoRAG 2 + PathRAG + OG-RAG)

- **HippoRAG 2** — personalized-PageRank substrate (~87-91% multi-hop recall, 10-30× cheaper than vanilla GraphRAG)
- **PathRAG** — flow-based path pruning (cuts context tokens ~44%)
- **OG-RAG** — ontology-gated synthesis (~40% fewer hallucinations via schema-constrained outputs)

### 8.2 Six retrieval strategies, agent-selected

Direct entity lookup · semantic text search · image similarity (CLIP) · k-hop graph walk · community retrieval (Leiden + LLM summaries) · bitemporal query. Fused via RRF, reranked with ColBERTv2 + Cohere Rerank 3.

### 8.3 World-model-conditioned ranking (Phase 2+)

Candidate nodes are scored by the World Model; retrieval ranking fuses semantic relevance + structural relevance + **role-conditioned prior** ("does this node's world-model role match the query intent?"). Biggest single recall lift on adversary-hiding queries.

### 8.4 Benchmark contract

- ≥ 90% multi-hop recall at ~50% of HippoRAG 2 context tokens with ~40% fewer hallucinations (versus rev. 2 numeric targets)
- Novelty score ≥ 4× best commercial alternative on published benchmark — **Phase 2 target, not launch target**

---

## 9. The Connection-Finding Engine (12 primitives)

10 primitives from rev. 2 (revised for hypergraph + distributional output) + 2 new in rev. 3:

1. `generate_hypotheses(seed_a, seed_b)`
2. `bounded_pathfind(seed_a, seed_b, max_hops=4, edge_filters, min_confidence=0.6)`
3. `probabilistic_path_score(path)` — Bayesian-smoothed, propagates distributional confidences
4. `community_co_membership(seed_a, seed_b, threshold=0.7)`
5. `temporal_coincidence(entities[], location_radius_m, time_window)`
6. `embedding_proximity_without_edges(seed, k=20)` — sock-puppet / alias detection
7. `structural_role_mining(entity)` — GraphSAGE structural embeddings, surfaces nexus entities
8. `contradiction_detect(entity)` — bitemporal reasoning over overlapping-valid-time edges
9. `predict_missing_link(entity, relation_type, top_k=10)` — Phase 1 via RotatE KGE; Phase 2 thin wrapper over Predictive Temporal's background scan
10. `cross_source_corroboration(claim, min_sources=3)` — Bayesian evidence triangulation with source-tier weighting (including C2PA / Amber Authenticate when present)
11. **`match_adversary_playbook(entity_or_subgraph)`** — ranks playbook matches with match-confidence distributions + distinguishing features + reference cases. *The most marketable single primitive.*
12. **`counterfactual_query(hypothesis)`** — distribution of graph states consistent with a hypothesis; returns discrepancy + consistency scores with evidence pointers. Phase 2 v1 (may be limited); Phase 3 v2.

### 9.1 Investigation templates

**"Find hidden attack surface"** (bounty-hunter-specific, entirely new workflow):
1. `match_adversary_playbook` on the target domain → does this look like a shadow-IT pattern?
2. `predict_missing_link(domain, *)` → what infrastructure should exist?
3. `structural_role_mining` on known assets → nexus hosts / forgotten infra
4. `embedding_proximity_without_edges` on sample employee → likely sock-puppet / forgotten accounts
5. `counterfactual_query` on top hypothesis → does it hold?
6. Synthesis: prioritized attack-surface report with citations

**"Are these the same actor?"** / **"What else should we look at?"** — same as rev. 3 templates, unchanged.

---

## 10. Agent Orchestration

**LangGraph** state machine: `Plan → Collect → Resolve → Traverse → Hypothesize → Verify → Synthesize → Report`. Every state replayable from event log.

**DSPy modules per node** — compiled prompts against eval traces, 99%+ reliability, cross-model portability.

**LLM Gateway** (§4-equivalent in rev. 3 §8.3): all LLM calls abstracted behind provider-agnostic gateway. Backends: Anthropic direct (primary, Claude Advisor Tool with Opus 4.7 advisor + Haiku 4.5 executor), OpenRouter (multi-model, cost-sensitive, continuous benchmark), BYOK (Operator tier). Per-call-site: preferred model, fallback chain, cost ceiling, benchmark-channel tagging.

**Claude Advisor + Policy Network dual voice** (Phase 3): at Plan/Pivot checkpoints, both voices produce ranked next-action suggestions. Agreement proceeds automatically; disagreement surfaces to user with one-click "take Advisor" / "take Policy" / "show me why."

**BAML** for all typed LLM I/O. **Audit trail + trajectory logging** — every state transition logged for replay and (Phase 3) Policy Network training.

---

## 11. The Compounding Benchmark

### 11.1 Composition

- **Internal benchmark (Phase 1):** 50 cases. Weekly run. MuSiQue + 2WikiMultiHop + TGB/TGB-Seq + curated real-world recon scenarios.
- **External-advisor-reviewed (Phase 2 months 6-10):** 200 cases. Monthly run. Three invited advisors (2 bounty hunters, 1 investigative journalist) validate methodology.
- **Public benchmark (Phase 2 month 10+):** 500 cases. Quarterly public report. Case definitions published; solutions not.

### 11.2 Metrics

- **Primary:** Novelty score — non-obvious, later-verified connections surfaced per case that baselines miss. "Non-obvious" defined as: not reachable via Tier 1 passive lookup on the seed entity.
- **Secondary:** multi-hop recall (MuSiQue-style), time-to-first-insight, cost per case, hallucination rate (fraction of claims with invalid provenance).

### 11.3 Baselines

- Maltego CE + standard transforms
- SpiderFoot OSS
- Shodan + Censys combined
- Stock Claude Opus 4.7 with generic MCP-OSINT tools (no world model, no adversary library)

The last baseline is the critical test of whether our architecture produces measurable advantage over a vanilla LLM + tools setup.

### 11.4 Publication philosophy

Internal-only for first 6 months of Phase 2 while methodology stabilizes under advisor review. Public launch only when the methodology can survive adversarial scrutiny. Quarterly thereafter — regression blocks release; engineering is partially compensated on benchmark deltas over rolling 6-month windows.

---

## 12. Tool Catalog

**Total at GA:** ~50 tools. Organized for bounty-hunter-first workflows.

### Phase 0 — 15 tools (months 0-3)

Stealth HTTP + domain recon + host intel — the bounty-hunter core.

1. `stealth_http_fetch` (rnet / JA4+ impersonation)
2. `subfinder_passive` (PD Go lib)
3. `dns_lookup_comprehensive` (dnsx)
4. `whois_query` (free tier + WhoisXML API)
5. `cert_transparency_query` (crt.sh)
6. `asn_lookup` (Team Cymru + BGPView)
7. `reverse_dns` (historical)
8. `http_probe` (httpx — tech fingerprint, screenshot, favicon hash)
9. `shodan_search`
10. `censys_search`
11. `port_scan_passive` (naabu)
12. `tech_stack_fingerprint` (webanalyze + Wappalyzer JSON)
13. `exposed_asset_find` (S3/GCS/Azure bucket + misconfig checks)
14. `leaked_secret_git_scan` (trufflehog + gitleaks against GitHub orgs)
15. `takeover_check` (common subdomain-takeover TLS/DNS signatures)

### Phase 1 — +20 tools (months 3-7) bringing total to 35

Identity, social, content/archives, images, corporate, meta.

- `username_search` (Go port of sherlock)
- `email_holehe` · `email_breach_lookup` (HIBP + DeHashed)
- `phone_numverify` · `google_account_ghunt`
- `instagram_public_profile` · `twitter_snscrape` · `linkedin_public_profile` · `reddit_api_query` · `github_user_profile`
- `intelx_search` · `dehashed_search`
- `opencorporates_search` · `opensanctions_screen` (via Nomenklatura) · `sec_edgar_filing_search`
- `wayback_history` · `common_crawl_lookup` · `pdf_document_analyze`
- `reverse_image_search` (Yandex + TinEye + Bing Visual)
- `exif_extract_geolocate`
- `evidence_bundle_export`

### Phase 2 — +10 tools (months 7-13) bringing total to ~45

Higher-value + intelligence-heavy.

- `gdelt_gkg_query` · `gdelt_visual_gkg_query`
- `deepfake_detect_media` (Reality Defender + Hive AI)
- `deepgram_transcribe_diarize` · `deepgram_voice_clone_detect`
- `arkham_entity_lookup` · `breadcrumbs_bridge_trace`
- `silentpush_passive_dns`
- `hunchly_sync` · `maltego_transform_bridge`

### Phase 3 — remaining (months 13+)

Face search (consent-gated multi-backend) · Etherscan / Solscan detailed traces · DarkOwl partnership · extended corporate adapters · watched-entity alerting.

### Tool-catalog open-source policy

**Apache-2.0:** all tool adapters in Phase 0 and most of Phase 1 (~30 tools). The MCP server shell, the typed tool protocol, the stealth HTTP layer, the evidence bundle exporter. These form the distribution surface.

**Proprietary (hosted only):** tools that depend on the World Model, Adversary Library, or Policy Network to produce value. The `match_adversary_playbook` tool, the `world_model_query` tool, `counterfactual_query`, `predict_missing_link` (as the continuously-trained variant), and anything that calls into federated aggregates. These stay closed because the training data and ongoing federation are the moat.

---

## 13. Infrastructure & Deployment

### 13.0 Scraping hierarchy (four-tier stealth ladder)

| Tier | Tech | Cost / request | Hits |
|---|---|---|---|
| 1 | Direct HTTP | ~$0 | Unprotected APIs |
| 2 | Stealth HTTP (rnet + JA4+) | ~$0 + proxy | ~30-40% of protected sites, no browser |
| 3 | Playwright + Nodriver | ~$0.002 | JS-heavy |
| 4 | Camoufox | ~$0.005 + premium proxy | DataDome / Turnstile / Akamai last-resort |

### 13.1 Runtime

- **Fly.io** primary — multi-region workers, single-tenant-per-VM initially
- **GCP** (existing account) — heavy batch (World Model training in Phase 2+, Splink model retrains)
- **Cloudflare** — DNS, R2, WAF, Turnstile

### 13.2 Proxy / data-plane

- BrightData residential (premium targets, metered)
- Oxylabs datacenter (bulk)
- Owned Hetzner DC rotating pool (€3/IP/mo, low-risk bulk)
- Tor (passive-only)
- Per-tenant API-key pool rotation (3-5 accounts per paid data source)

### 13.3 Data-plane caching

Every external API response cached to R2 by normalized-query hash with source-specific TTL. Cross-tenant cache sharing only on deterministic public-data sources (WHOIS, DNS, cert transparency, Wayback, Common Crawl). Commercial-dataset responses never cross-tenant-cached.

### 13.4 Observability

OpenTelemetry → Grafana Cloud · Sentry · ClickHouse (Phase 2+ optional for billing/analytics).

### 13.5 Security

- mTLS inter-service · Ed25519-signed internal calls
- Postgres RLS + tenant ID in every query
- FalkorDB graph-per-tenant in Phase 0-1; pooled multi-tenant in Phase 2
- R2 artifact paths: tenant + SHA-256; short-lived signed URLs
- Secrets via Infisical (self-hosted on Fly) or Google Secret Manager
- Bug bounty program launches Phase 2 (pays bounty hunters in credits — perfect buyer feedback loop)

### 13.6 Cost model (revised for solo + single-tenant-per-VM early)

| Item | 100 paid users | 1,000 paid users | 5,000 paid users |
|---|---|---|---|
| Fly.io compute (single-tenant-per-VM early; pooled at 1K+) | $400 | $4,000 | $12,000 |
| Postgres (Fly.io Postgres or Supabase) | $100 | $400 | $1,500 |
| FalkorDB (self-hosted on Fly) | $100 | $500 | $2,000 |
| DragonflyDB | $50 | $200 | $800 |
| R2 storage + egress | $40 | $250 | $1,200 |
| Proxy pool | $400 | $2,500 | $10,000 |
| Third-party API licenses | $600 | $3,500 | $15,000 |
| LLM (Claude Advisor via Gateway) | $400 | $3,000 | $12,000 |
| GPU training (Phase 2+) | $0 | $800 | $4,000 |
| GPU inference (Phase 2+) | $0 | $400 | $2,500 |
| **Total infra** | **~$2,090** | **~$15,550** | **~$61,000** |
| **Revenue @ avg $85/user** | **$8,500** | **$85,000** | **$425,000** |
| **Gross margin** | ~75% | ~82% | ~86% |

At 100 paid users (month 4-5), you're ramen-profitable solo. At 1K (month 10-14), this is a real business. At 5K (year 2+), it's fundable or bootstrapped-profitable.

---

## 14. Non-Functional Requirements

| Category | Target |
|---|---|
| MCP tool call p95 (cached) | < 200ms |
| MCP tool call p95 (uncached) | < 4s |
| Multi-hop connection-find p95 | < 8s (up to 4 hops, tenant graph ≤ 10M nodes) |
| Investigation end-to-end (medium) | 5-15 min |
| Uptime | 99.5% Phase 0; 99.9% Phase 2+ |
| World Model scoring p95 (sync) | < 50ms (Phase 2+) |
| World Model scoring p95 (async) | < 500ms (Phase 2+) |
| Predictive Temporal background scan | hourly per tenant, < 10 min (Phase 2+) |
| Federated aggregation cadence | daily (Phase 2+) |
| DP ε per release | published; ≤ 1.0 structural, ≤ 2.0 meta-features (revised down from rev. 3's 4.0 pending expert review) |
| Benchmark cadence (public) | quarterly (Phase 2+) |
| Benchmark cadence (internal) | weekly (Phase 0+) |
| DSAR response | < 30 days (GDPR) |
| Evidence chain immutability | R2 object-lock + Merkle rollups |

---

## 15. Distribution Strategy (Solo-Founder PLG Stack)

Each channel below is executable by a solo technical founder with **zero sales skill, zero outbound, zero contacts**. Precedent in the right-hand column proves the pattern works.

| Channel | Execution | Proven-by-example |
|---|---|---|
| **Open-source core** (Apache-2.0: MCP server + 30 tools + orchestration glue) | GitHub-first. Docs as marketing. Weekly release cadence. | Supabase, Sentry, PostHog, Plausible, Cal.com |
| **MCP Registry listings** (Anthropic official, Smithery, MCP.so) | Submit once per registry. Passive distribution on a channel that's < 18 months old. | First-mover advantage window |
| **Hacker News launch posts** (architecture deep-dives) | Write-once, submit, technical depth sells itself. Target one "Show HN" every 2-3 months. | Fly.io launch, Val.town, Resend |
| **Twitter/X "building in public"** | Weekly thread: investigation screenshot + what the tool found. No personality required. | Pieter Levels, Cal.com, DHH |
| **YouTube case-study demos** | 10-min screen-record + narration. No face. Each video = compounding SEO asset. | NahamSec, InsiderPhD, STÖK |
| **Case-study blog (SEO compound)** | Weekly: "I used [tool] to find [hidden thing] in N minutes." Ranks on Google for "how to find X." | Bellingcat training blog, Huntress, GreyNoise |
| **Conference CFPs (free)** | BSides (multi-city), DEFCON Recon Village, NahamCon, SANS OSINT Summit. Prep once, deliver many times. | Every bounty-hunter tool that matters |
| **Discord community participation** | Bugcrowd, HackerOne, OSINT Curious, InfoSec Prep. Answer questions; don't spam. | Caido, every indie security tool |
| **Podcast appearances** | Darknet Diaries, Risky Business, DayZero, Privacy-Security-OSINT Show. Pitch once, accepted repeatedly. | Free media for technical founders |
| **In-workflow integrations** | Burp Suite plugin, Caido plugin, Discord webhooks, Obsidian/Notion export. Distribution embedded in product. | Tailscale, 1Password, Vanta |
| **Bounty program that pays in credits** (Phase 2) | Bug bounty hunters find bugs; we pay in product credits. Every bounty = product feedback + evangelism. | Dual-purpose: security + marketing |
| **Journalist free-tier program** | Verified-journalist free Hunter accounts in exchange for attribution. One ProPublica byline = 10K signups. | Muckrock, DocumentCloud growth patterns |

**Explicitly NOT in the stack (for sanity):**
- Outbound sales (email, LinkedIn, cold call) — ever
- Paid advertising (Google, LinkedIn, Reddit ads) — until MRR > $50K
- Conferences as exhibitor (vs speaker) — requires booth staffing
- Partnership negotiations with data providers — use public/self-serve APIs
- Influencer marketing — requires negotiation
- Enterprise sales — requires sales team

**Success criteria per channel:**
- Months 0-3: 100 GitHub stars, 500 MCP server installs, 1 Hacker News front-page post, 1 ProPublica/Bellingcat mention, 100 Free users, 10 paying.
- Months 3-7: 1K GitHub stars, 5K installs, 2 HN front-page, 3 conference talks accepted, 500 Free, 50 paying.
- Months 7-13: 5K stars, ramen-profitable, first VC inbound (reject or raise on terms), 200+ paying, compounding-benchmark story viral.

---

## 16. Open-Source Strategy

### 16.1 What's Apache-2.0 (public code)

- **MCP server shell** — stdio + Streamable HTTP transport, auth passthrough, tool dispatch, credit-metering stubs
- **Tool adapters (~30)** — all Phase 0 + most of Phase 1 tools
- **Stealth HTTP layer** (rnet wrapper with JA4+ impersonation presets)
- **Typed tool protocol + SDKs** (TS, Go, Python)
- **Agent orchestration glue** (LangGraph recipes, BAML templates, DSPy module examples)
- **CLI shell + plugin harness**
- **Evidence bundle exporter** (format + verifier, even if signing service is hosted)
- **Example adversary playbooks** (~5 simple ones to illustrate format — the library proper stays closed)
- **Benchmark harness code** (not the case data — the harness runner)

### 16.2 What's proprietary (hosted-only)

- **World Model** — training code, model weights, inference service
- **Adversary Library** — full 20+ playbooks in v0, 60+ in v1, 150+ in v2
- **Federated Learning aggregator** — secure aggregation, DP budget tracking, cross-tenant priors
- **Predictive Temporal Layer** — TGN training + background scanner
- **Investigator Policy Network** — training + serving (Phase 3)
- **Pattern Library (Tier 2 knowledge store)** — aggregated cross-tenant patterns
- **Hosted data plane** — proxy pool, API-key rotation pool, cache
- **The benchmark case dataset itself** — harness is open; cases are not

### 16.3 Why this split works

The OSS pieces are **the distribution channel**. The closed pieces are **the moat**. Every happy OSS user is a potential paying user who wants the moat features (adversary matching, world-model-conditioned retrieval, federated priors, predictive temporal) that cannot be reproduced without aggregate cross-tenant data.

**Forks are not a threat** because:
1. A fork cannot federate across users without access to the aggregated patterns.
2. A fork cannot call the World Model without running the training pipeline on aggregate data it doesn't have.
3. A fork without those is strictly less useful than the hosted version, and users know it.

### 16.4 Contributor strategy

- Clear `CONTRIBUTING.md` with well-labeled "good first issue" pipeline
- Monthly community call (30 min, recorded, YouTube)
- Contributors get Hunter-tier credits as thank-you
- Most-valuable contributors get Operator-tier credits
- Adversary Library PR pathway: accept playbook contributions through a review process; contributor gets credit + co-author attribution in the case-study blog

---

## 17. Phasing & Milestones (solo-founder-sized)

Four phases. Weekly/bi-weekly public build log from day one.

### Phase 0 — Credible MVP (months 0-3)

**Goal:** Ship a usable, paid OSS product. Hacker News launch. First 10 paying customers.

- Foundation: monorepo, Fly.io + Cloudflare + Postgres + R2, Stripe Free + Hunter tiers, OpenTelemetry
- LLM Gateway (Anthropic + OpenRouter backends, cost-ceiling, fallback chain)
- MCP server (stdio + Streamable HTTP) — open-sourced Day 1
- **15 Phase 0 tools** (stealth HTTP + domain/infra + host intel + git secret scan + takeover check)
- FalkorDB + Graphiti + **hypergraph data model + distributional edges** (foundational, done right the first time)
- Three-tier ER (GLiNER + Jellyfish-8B + Claude Haiku) — basic
- Trajectory / event stream logging (inert — feeds future learning loops)
- CLI binary
- Minimal docs site (Starlight or similar)
- **Launch beats: Show HN · first YouTube demo · first case-study blog post · Twitter build log active**
- Target exit: 100 Free users, 10 paying ($500 MRR)

### Phase 1 — Compounding foundation ships + Operator tier (months 3-7)

**Goal:** Full v1 tool catalog, 6 connection primitives, Adversary Library v0, ship Operator tier, grow to 50 paying.

- +20 tools → **35 total**
- **6 of 12 connection primitives** live: `bounded_pathfind`, `probabilistic_path_score`, `generate_hypotheses`, `temporal_coincidence`, `community_co_membership`, `cross_source_corroboration`
- **Adversary Library v0** — 20 playbooks curated by Jason, subgraph matching engine live
- **`match_adversary_playbook` primitive** ships (priced into Operator tier)
- Composite retrieval (HippoRAG 2 + OG-RAG — PathRAG deferred to Phase 2)
- LangGraph + BAML orchestration with Claude Advisor Tool
- Operator tier launches ($199/mo)
- Web UI (SvelteKit, minimal — case browser, graph view, report export)
- REST API live
- **Internal benchmark v1** (50 cases, weekly run)
- **Launch beats:** 2nd HN post ("Ship: Adversary-aware OSINT for bounty hunters"), first conference talk, Burp Suite plugin ships, first journalist attribution
- Target exit: 500 Free users, 50 paying ($3-5K MRR), 1K GitHub stars

### Phase 2 — Compounding layer activates (months 7-13)

**Goal:** The moat goes live. World Model v1 + Federated Learning Buckets A+B + Predictive Temporal. Remaining primitives. Ramen-profitable.

- **Remaining 6 primitives** ship: `embedding_proximity_without_edges`, `structural_role_mining`, `contradiction_detect`, `predict_missing_link` (KGE v1), `match_adversary_playbook` (if not in Phase 1), `counterfactual_query` v1
- **World Model v0.5 → v1.0** — structural GraphSAGE layer activates, then behavioral sequence model on opt-in trajectories
- **Federated Learning Buckets A + B** active with published DP ε budgets; independent crypto audit complete
- **Adversary Library v1.0** — 60+ playbooks (community + federated discovery + curation)
- **Predictive Temporal v1.0** — TGN background scan, arrival-time predictions, lead surfacing
- **PathRAG pruning** integrated
- **DSPy compilation** active on core prompts
- **10 Phase 2 tools** added (GDELT, Deepfake detection, audio OSINT, Arkham/Breadcrumbs, Silent Push) → **~45 total**
- Multi-tenant pooling (exit single-tenant-per-VM)
- Public benchmark launch (500 cases, quarterly reports begin)
- **Launch beats:** "We got 18% smarter this quarter with zero code changes" viral blog post, DEFCON Recon Village talk, first journalist investigation using the tool becomes a major story
- Target exit: 2K Free users, 200 paying ($15-20K MRR, ramen-profitable solo), 5K GitHub stars

### Phase 3 — Policy Network + dominance (months 13-24)

**Goal:** Investigator Policy Network ships. Tool exceeds expert-investigator baseline on the public benchmark. Decision point: raise / stay indie / sell.

- **Investigator Policy Network v1.0** — imitation learning on ~10K opt-in resolved trajectories
- **Policy Network v2.0** (later in phase) — offline RL (Conservative Q-Learning) on verdict rewards
- **Adversary Library v2.0** — 150+ playbooks, continuous discovery mature
- **World Model v2.0** — vertical-specific sub-models (bounty-hunter specialization, journalist specialization, crypto-investigation specialization)
- TypeDB evaluation + migration for Compliance-tier workloads if justified
- Real-time watched-entity alerting with predictive arrival times
- Team tier + SSO *if ≥ 50 paying users demand it*
- Compliance tier *if inbound demand arrives*
- Bug bounty program launches
- **Launch beats:** "Our policy network beats top investigators on the benchmark" public report, potential fundraising round or deliberate bootstrap continuation
- Target exit: $50K+ MRR, approaching $1M ARR, optionally fundable at $50M+ pre-money

---

## 18. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| **Solo-founder burnout / single-point-of-failure** | High | Catastrophic | Ruthless scope discipline (this spec). Automated testing from Day 1. Hire first contractor (not FTE) at $10K MRR; first engineer at $30K MRR. |
| **OSS channel doesn't produce distribution** | Medium | High | Triple-diversified GTM (OSS + content + community). Weekly output. If still zero signal at month 4, reassess buyer assumption. |
| **Bug bounty hunters don't adopt despite feature-fit** | Low | High | Pre-build validation: post architecture concept on r/bugbounty + Twitter in month 1; observe pull. If zero resonance, pivot buyer before building more. |
| **Legal action from scraped platforms** | Medium | High | Conservative scraping posture, stealth HTTP tier never bypasses auth, AUP enforced, legal counsel on retainer (~$500/mo) |
| **Breach / case data leak** | Low | Catastrophic | Tenant isolation, RLS, encryption, bug bounty program Phase 2 |
| **Federated learning privacy breach** | Low-Medium | Catastrophic | Published ε budgets, secure aggregation, independent crypto audit before Channel B/C activation, k≥20 anonymity, red-team attack simulation |
| **LLM cost shock** | Medium | Medium | LLM Gateway cost ceilings + OpenRouter fallback + aggressive prompt caching + Haiku-first routing |
| **Third-party API price shocks** | Medium | Medium | Multi-source redundancy, cache amortization, open-source alternatives for each paid source where possible |
| **Competitors copy OSS and hosted features** | Medium | Low | OSS forks cannot federate; moat is in aggregate data, not code. Shrug and ship faster. |
| **FalkorDB / Graphiti project health** | Low | High | Graph layer behind abstraction; Neo4j + TypeDB fallback implementations in CI |
| **Abuse (stalking, doxing)** | Medium | Catastrophic (reputational) | AUP + programmatic anomaly detection + red-flag query review + account suspension |
| **Policy Network data volume insufficient by Phase 3** | Medium | Medium | Tiered opt-in incentives (credit rebates, pending GDPR legal review), seed with internal investigations Jason runs, synthetic-case augmentation |
| **Hypergraph emulation performance degrades** | Medium | Medium | Measurement-contingent migration plan (§4.6); TypeDB evaluation Phase 2 |
| **Solo founder isn't actually good at technical execution this ambitious** | ??? | Catastrophic | Ship Phase 0 in 3 months as self-validation. If Phase 0 slips > 50%, scope is wrong — cut, don't push. |

---

## 19. Out of Scope (v1)

Permanently or until inbound demand forces a revisit:

- Law enforcement / government customers (separate compliance program required)
- On-prem / air-gapped deployment (Compliance tier might re-raise)
- Mobile apps (web + CLI + MCP covers the workflow)
- Real-time surveillance / alerting at sub-hourly cadence (Phase 3+)
- Marketplace for user-contributed tools (Phase 3+)
- Voice biometric matching (Phase 3+ if demand materializes)
- Satellite imagery deep analysis (Phase 3+ via Sentinel Hub / Planet Labs partnership if ever)
- Multi-seat Team accounts + SSO (only if ≥ 50 paying users request)
- SOC 2 / ISO 27001 certification (only if a Compliance customer funds it)

---

## 20. Open Questions

1. **Branding / name** — `osint-agent` placeholder. Candidates: Meridian, Argus, Telltale, Nexus, Beacon, Clue, Quarry, Tradecraft. Given adversary-aware positioning, *Quarry* and *Tradecraft* lean into the frame. *Meridian* and *Clue* are softer. Recommendation: decide before Phase 0 Show HN.
2. **Bucket C opt-in rebate structure** — current design (15% credit rebate for opt-in) may fail GDPR "freely given consent" test. Legal review required before Phase 2 activation. Alternative: reframe as community-contribution status (badge, recognition, NOT financial incentive) — slower data accumulation but cleaner legal posture.
3. **Public benchmark external advisor selection** — who serves? Recommendation: solicit via Twitter in Phase 1 ("looking for 3 volunteer advisors — 2 bounty hunters, 1 investigative journalist — to help validate our upcoming public benchmark methodology").
4. **First contractor hire timing and skillset** — at $10K MRR (realistic Phase 1 exit), likely a DevOps / SRE contractor (10hrs/week, $2-3K/mo) to handle infra oncall while Jason builds. First FTE at $30K+ MRR, probably an ML engineer to own the World Model / Federated Learning stack in Phase 2.
5. **Bounty hunter validation experiment** — in month 0-1, post the architecture concept on `/r/bugbounty`, Twitter, and relevant Discords. If resonance is weak, reconsider buyer assumption before committing 3 months to Phase 0.

---

## 21. Candor — What Rev. 4 Costs and What It Assumes

### Costs (honestly named)

**Technical ambition vs one-person execution.** Rev. 4 still assumes one person builds six integrated systems (MCP + tool platform + graph + ER + retrieval + agent orchestration) in 3 months for Phase 0. Plausible but tight. **If Phase 0 slips > 50% beyond 3 months, the plan is wrong — cut scope, don't push timeline.**

**No sales safety net.** If the bounty-hunter wedge doesn't resonate, there is no sales team to pivot to enterprise buyers to extend runway. Validate the wedge cheaply in month 0-1 before committing.

**Operator-tier pricing exposure.** $199/mo depends on Adversary Library + Predictive Temporal actually being valuable enough to justify the premium. If Phase 1 ships Operator features that Hunter-tier users don't upgrade for, the pricing model is wrong — drop Operator to $99 and re-segment.

**Federated learning legal exposure.** DP budgets, secure aggregation, and the opt-in consent structure all require real legal review. Cannot be self-served. Budget ~$3-5K for legal consultation before Phase 2.

### Assumptions (if any are wrong, plan breaks)

1. **Bug bounty hunters will adopt a PLG OSINT tool at $49-$199/mo.** Validate in month 0-1 via audience posts.
2. **OSS + content + MCP registries + conference talks produce enough distribution for a solo founder to reach $15-20K MRR within 13 months.** Diversified channels reduce risk but any individual channel may disappoint.
3. **The compounding architecture (World Model + Adversary Library + Federated Learning) is buildable by one person in Phases 1-2 at the ambition level spec'd.** Contingency: scope down to structural layer only; delay behavioral + Policy Network.
4. **Anthropic / OpenRouter / Fly.io / FalkorDB / Graphiti all remain operationally healthy through the build window.** Mitigation: abstraction at every provider boundary.
5. **Jason personally has the domain expertise to curate 20 adversary playbooks at Phase 1 launch.** If not, delay `match_adversary_playbook` to Phase 2 and partner with one or two external curators in exchange for Operator credits.

### The unique advantage Jason brings

You are not a typical solo founder building an OSINT tool. You have deep AI-systems expertise (Head of Engineering at Vurvey, multi-agent / agentic market research production experience), direct hands-on with Claude agentic patterns, cheap GCP compute access, a taste-validated technical stack, and the operator instinct to ship aggressively. **The compounding-inference architecture is precisely the kind of product where the winner is determined by the builder's conviction and shipping velocity, not by headcount.** Rev. 4 is sized for one person to win if — and only if — that person executes aggressively and cuts scope honestly.

---

## 22. Appendix — References and Glossary

### A. Reference reading

- HippoRAG 2 · PathRAG · OG-RAG composite retrieval
- FalkorDB GraphBLAS architecture + multi-tenant isolation
- Graphiti bitemporal KG design (Zep)
- Splink Fellegi-Sunter ER (UK MoJ)
- Jellyfish-8B / Jellyfish-13B data preprocessing LLM
- Claude Advisor Tool (Anthropic 2026)
- MCP spec v2.1 + Server Cards
- GLiNER-Relex extraction
- RotatE / ComplEx / DistMult KGE for link prediction
- TGN / TGN-SEAL temporal graph networks · TGB / TGB-Seq benchmarks
- ProjectDiscovery Go library suite (subfinder, httpx, dnsx, naabu, katana)
- JA4+ TLS fingerprinting (FoxIO)
- rnet / curl_cffi / pyreqwest-impersonate HTTP clients
- LangGraph + DSPy + BAML composition pattern
- GraphSAGE inductive embeddings for structural role mining
- VF3 subgraph matching + neural subgraph matching
- Flower federated learning + homomorphic secure aggregation
- Differential Privacy literature (Abadi et al., Dwork-Roth textbook)
- Content Authenticity Initiative / C2PA / Amber Authenticate
- Nomenklatura data-integration toolkit (OpenSanctions)
- Waymo compounding-data precedent (autonomous driving parallel)
- Supabase / Sentry / PostHog / Plausible / Cal.com / Fly.io OSS-PLG case studies

### B. Glossary

- **Adversary Library**: curated + learned corpus of behavioral playbooks (shell structures, sock-puppet topologies, shadow infrastructure patterns) matched against tenant graphs via subgraph matching.
- **Bitemporal**: modeling valid-time (true-in-world) + system-time (recorded).
- **Bucket A / B / C**: three tiers of cross-tenant learning contribution (§4.2).
- **Compounding Inference Layer**: the architectural stack (§4) that makes the product strictly smarter over time.
- **DP / ε**: differential privacy / privacy-loss budget.
- **Federated Learning**: cross-tenant learning with cryptographic isolation; patterns learned globally, case data stays local.
- **Hypergraph**: graph where edges can connect ≥ 2 nodes (N-ary), emulated on FalkorDB via event nodes.
- **JA4+**: TLS/HTTP fingerprint family (FoxIO, 2023), universal anti-bot standard 2026.
- **LLM Gateway**: internal abstraction in front of Anthropic / OpenRouter / BYOK backends.
- **MCP**: Model Context Protocol — standard interface between LLMs and external tools.
- **Novelty Score**: primary benchmark metric — non-obvious, later-verified connections per case that baselines miss.
- **PLG**: Product-Led Growth — distribution via product quality + viral loops, not sales.
- **Policy Network**: next-action model trained on resolved-case trajectories (Phase 3).
- **TGN**: Temporal Graph Network — neural model for dynamic graphs.
- **World Model**: learned probabilistic model over population-scale entity / relationship / behavior distributions, queried as priors for Bayesian updates.
