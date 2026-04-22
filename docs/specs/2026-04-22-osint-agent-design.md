# OSINT Agent — System Design Spec

**Date:** 2026-04-22 (rev. 2 — post-SOTA verification pass)
**Author:** Jason Roell
**Status:** Draft — awaiting review
**Codename:** TBD (placeholder: `osint-agent`)

---

## 1. Executive Summary

A commercial, AI-agent-first Open Source Intelligence (OSINT) platform targeted at solo investigators, private investigators, and freelance journalists. The product ships first as a **Model Context Protocol (MCP) server** that any LLM client (Claude Desktop, Cursor, ChatGPT Desktop, etc.) can connect to, exposing ~50 curated OSINT tools + a purpose-built **connection-finding engine** backed by a multi-tenant bitemporal knowledge graph. Phase 2 layers a first-party web analyst UI on top of the same backend.

The core bet: OSINT tool execution is commodity; **the ability to find non-obvious, multi-hop connections across disjoint data sources with citation-grade evidence is not**. We purpose-build the data, retrieval, and reasoning layers around that capability.

### Primary buyer
Solo PIs, freelance investigative journalists, small investigations firms. Self-serve credit-card sign-up. Target ACV range: $40–200/month base + usage-based credits.

### Competitive positioning
- Cheaper than Skopenow / OSINT Industries / IRBsearch at the solo-investigator tier.
- Smarter (AI-native) than Maltego CE / SpiderFoot OSS / free alternatives.
- Narrower and faster than enterprise Social Links / ShadowDragon.
- **Unique moat:** bitemporal graph + probabilistic entity resolution + 8 connection-finding primitives exposed as typed tools to any MCP-compatible LLM.

---

## 2. Product Shape

### 2.1 Interfaces

| Interface | Who | Ships in |
|---|---|---|
| MCP server (stdio local) | Individual LLM clients on the user's laptop | Phase 1 |
| MCP server (Streamable HTTP, remote) | Hosted LLM clients (ChatGPT web, Claude web) | Phase 1 |
| REST API + API keys | Power users scripting investigations | Phase 1 |
| Web analyst UI | Users who want a dedicated workbench | Phase 2 |
| CLI (`osint-agent`) | Technical users | Phase 2 |

### 2.2 Deployment model

Thin local MCP client that authenticates the user against the hosted backend. All expensive / regulated / cache-worthy operations (third-party API calls, proxy pool use, evidence storage, graph persistence) run in the hosted backend. Premium tier permits Bring-Your-Own-Key (BYOK) for compliance-sensitive customers.

### 2.3 Pricing (A3 — subscription + credit packs)

| Tier | Monthly | Included credits | Features |
|---|---|---|---|
| Starter | $39 | 2,000 credits | Core tools, 3 concurrent investigations, 30-day retention |
| Pro | $129 | 10,000 credits | All tools, 20 concurrent, 180-day retention, contradiction detection, community analysis |
| Team | $399 | 40,000 credits | 5 seats, shared cases, 1-year retention, priority queue |
| BYOK add-on *(Phase 2)* | +$49 | — | Bring your own API keys for compliance-sensitive work |

**Credits** meter tool invocations, LLM tokens, and proxy bandwidth on a unified scale. One credit = retail $0.01 (the retail price already incorporates our ~3× markup over underlying cost). Included-credit allocations above reflect retail value (Starter = $20 in included retail usage). Overage billed at 1.2× retail rate. Credit-pack purchases ($10–500) for bursty investigations.

### 2.4 Compliance posture (B2 — pragmatic)

**We will:**
- Aggregate public-web data permitted by source-of-record ToS.
- Query licensed paid APIs with proper commercial contracts.
- Passive recon (DNS, cert transparency, WHOIS, OSINT datasets).
- Social media *public* data through each platform's official or licensed commercial-research API where available; fall back to humane, rate-limited scraping for platforms with research-friendly ToS.
- Provide chain-of-custody audit trails on every artifact (hash, timestamp, source URL, retrieval method).

**We will not:**
- Bypass authentication walls, CAPTCHAs, or rate-limit evasion of prohibited platforms.
- Aggregate breached credential data in plain text (hashed/partial only; links to HIBP-style services).
- Store PII beyond what the user explicitly ingested for a case.
- Serve customers in sanctioned jurisdictions (OFAC screening on sign-up).
- Tolerate stalking, harassment, doxing as use cases (enforced AUP + human review escalation).

**Legal baseline:**
- SOC 2 Type I within 12 months, Type II within 24.
- GDPR- and CCPA-compliant DSAR flow (customers can export/delete their case data).
- Published Acceptable Use Policy + KYC-lite sign-up (verified email, payment method, attestation).

---

## 3. System Architecture — Layered View

```
┌─────────────────────────────────────────────────────────────┐
│  User's LLM client (Claude Desktop / Cursor / ChatGPT)      │
└─────────────────────────┬───────────────────────────────────┘
                          │ MCP (stdio or Streamable HTTP)
┌─────────────────────────▼───────────────────────────────────┐
│  Thin local MCP client (TS) — auth + tool proxy             │
└─────────────────────────┬───────────────────────────────────┘
                          │ HTTPS (signed requests)
┌─────────────────────────▼───────────────────────────────────┐
│  Edge: Cloudflare (WAF, rate-limit, Turnstile)              │
└─────────────────────────┬───────────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────────┐
│  Product API (Bun + TS + ElysiaJS)                          │
│  - Auth / billing / case mgmt / credit metering             │
│  - MCP-over-HTTP endpoint                                   │
│  - REST API for power users                                 │
└──┬──────────────────┬──────────────────┬────────────────────┘
   │                  │                  │
   │                  │                  │
┌──▼────────────┐ ┌───▼────────────┐ ┌───▼────────────────────┐
│ Tool workers  │ │ Browser        │ │ Python workers         │
│ (Go)          │ │ workers (Node) │ │ (FastAPI + uv)         │
│ PD libs,      │ │ Playwright +   │ │ holehe, maigret,       │
│ DNS, HTTP,    │ │ Nodriver +     │ │ ghunt, snscrape,       │
│ cert transp.  │ │ Camoufox       │ │ instaloader-specifics  │
└──┬────────────┘ └───┬────────────┘ └───┬────────────────────┘
   │                  │                  │
   └──────────────────┴──────────────────┘
                      │
                      │ Tool-result protocol (typed JSON, signed)
                      │
┌─────────────────────▼───────────────────────────────────────┐
│  Ingest / Entity Resolution pipeline                        │
│  - GLiNER + GLiNER-Relex (fast deterministic NER+RE)        │
│  - Claude Haiku 4.5 via BAML (ambiguous cases)              │
│  - libpostal, libphonenumber, tldextract normalizers        │
│  - Splink probabilistic record linkage (dedupe + link)      │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│  Knowledge layer                                            │
│  ┌──────────────────────────────────────┐                   │
│  │ FalkorDB (multi-tenant graph,        │ ◄── Graphiti      │
│  │ GraphBLAS engine, native vectors,    │     (bitemporal   │
│  │ Cypher)                              │      layer)       │
│  └──────────────────────────────────────┘                   │
│  ┌──────────────────────────────────────┐                   │
│  │ PostgreSQL 16 (OLTP + evidence meta) │                   │
│  │ + pgvector + pgvectorscale           │                   │
│  └──────────────────────────────────────┘                   │
│  ┌──────────────────────────────────────┐                   │
│  │ Cloudflare R2 (immutable artifacts)  │                   │
│  └──────────────────────────────────────┘                   │
│  ┌──────────────────────────────────────┐                   │
│  │ DragonflyDB (cache + rate limit)     │                   │
│  └──────────────────────────────────────┘                   │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│  Retrieval & Reasoning layer                                │
│  - Hybrid retrieval (BM25 + vector + graph walk)            │
│  - HippoRAG 2 personalized-PageRank pattern                 │
│  - Community summaries (Leiden + LLM synthesis)             │
│  - ColBERTv2 + Cohere Rerank 3 final stage                  │
│  - RRF fusion                                               │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│  Agent orchestration                                        │
│  - LangGraph (TS) state machine                             │
│  - Claude Advisor Tool (Opus 4.7 + Haiku 4.5)               │
│  - BAML typed outputs                                       │
│  - 8 Connection-Finding primitives (MCP tools)              │
└─────────────────────────────────────────────────────────────┘

Observability sidecar: OpenTelemetry → Grafana Cloud (traces, logs, metrics) + Sentry (errors).
Job substrate: River (Go) on Postgres for background scans, long-running investigations, scheduled retrieval.
```

---

## 4. Data Model

### 4.1 Canonical entity types (v1)

Every extracted atom resolves to one of these typed nodes. Types are extensible via a registry.

| Entity | Key attributes | Normalizer |
|---|---|---|
| `Person` | name (parsed), DOB (ISO date + precision: `year` / `year-month` / `full` + confidence), aliases[] | `nameparser` |
| `EmailAddress` | local-part, domain, canonical | built-in |
| `PhoneNumber` | E.164, country, carrier (inferred) | `libphonenumber` |
| `PhysicalAddress` | parsed components, lat/lng, confidence | `libpostal` + geocoder |
| `Domain` | apex, subdomain, IDN-normalized | `tldextract` + `idna` |
| `IPAddress` | v4/v6, ASN, geolocation | `ipinfo`/`ip2asn` |
| `Organization` | legal name, jurisdiction, registration ID | custom |
| `SocialAccount` | platform, handle, URL, display name | per-platform |
| `Document` | SHA-256, MIME, exif, source | built-in |
| `Image` | SHA-256, perceptual hash, CLIP embedding | `imagehash` + CLIP |
| `CryptoWallet` | chain, address, format, ENS | `ens-normalize` + checksums |
| `Event` | type, timestamp, location, participants | custom |
| `Claim` | subject, predicate, object, source, confidence | — |

### 4.2 Edge types (sample)

`HAS_EMAIL`, `HAS_PHONE`, `LIVES_AT`, `OWNS`, `CONTROLS`, `EMPLOYED_BY`, `SHARES_ADDRESS_WITH`, `APPEARS_IN_BREACH_WITH`, `MENTIONED_WITH`, `SAME_PERSON_AS` (probabilistic), `REGISTERED_DOMAIN`, `AT_LOCATION`, `TRANSACTED_WITH`, `CITED_IN`, `CONTRADICTS`.

Every edge carries:
- `confidence` (0.0–1.0, calibrated — not raw)
- `valid_from`, `valid_to` (world-time)
- `observed_at`, `superseded_at` (system-time — bitemporal)
- `source` (Claim ID → provenance chain)
- `method` (how the edge was derived — extraction, inference, user-asserted)

### 4.3 Chain-of-custody / Claim model

Every assertion in the graph is backed by a `Claim` that records:
- Raw artifact hash (pointer to R2 object)
- Retrieval timestamp (system-time)
- Tool that produced it + tool version
- Proxy/IP used (for reproducibility + dispute)
- Agent reasoning trace ID (for LLM-derived claims)
- User who initiated the investigation

This is court-admissible-grade provenance. It is also *the* feature journalists need to publish.

### 4.4 Bitemporal semantics (Graphiti layer)

- **Valid-time**: when the fact was true in the world. "John lived at 123 Main St from 2022-03 to 2024-07."
- **System-time**: when we learned it. "We ingested this fact on 2026-04-22 at 14:30 UTC from source X."
- Every node and edge has four timestamps. Contradiction detection is a trivial bitemporal query: two edges with overlapping valid-time but incompatible attributes, observed from different sources.

---

## 5. Entity Resolution (ER) Pipeline

The ingest pipeline that makes everything else work.

### 5.1 Stages

1. **Extract (tiered)** — three-tier routing based on input complexity:
   - **Tier 1 (~80%):** **GLiNER + GLiNER-Relex** — deterministic encoder-only NER + relation extraction. ONNX runtime, hardware-cheap. Confidence scores native.
   - **Tier 2 (~18%):** **Jellyfish-8B** (Llama-3 base) self-hosted on GPU. Instruction-tuned for entity matching / data preprocessing. ~0.08s per instance on A100. Rivals GPT-4 on EM benchmarks at a fraction of the cost. Handles ambiguous pair-decisions and medium-context cases.
   - **Tier 3 (~2%):** **Claude Haiku 4.5 via BAML** — only invoked when reasoning over long context or cross-document inference is required. Typed outputs.
2. **Normalize** — domain-specific normalizers (addresses via `libpostal`, phones via `libphonenumber`, domains via `tldextract` + `idna`, crypto via `ens-normalize`) produce canonical forms.
3. **Candidate-match** — Splink generates candidate pairs using blocking rules (e.g., same domain, same phone prefix, nearby embedding).
4. **Probabilistic scoring** — Fellegi-Sunter m-probabilities and u-probabilities per comparison vector, producing match-weight per candidate pair.
5. **Thresholded merge** — three outcomes: auto-merge (above threshold), review-queue (ambiguous), reject.
6. **Graph-write** — transactional write to FalkorDB + Graphiti with full provenance.

### 5.2 Per-tenant models

Splink models are tenant-scoped — a PI investigating industry A has different match weights than one investigating industry B. Base models ship pre-trained; tenants implicitly train through merge/reject decisions.

### 5.3 SLA

- p95 ingest latency (single artifact → graph, per-tenant graph ≤ 10M nodes): **< 2s**
- Throughput per worker: **≥ 50 artifacts/s**
- ER precision target: **≥ 0.98** (we favor precision over recall — a false merge corrupts the entire tenant graph)
- ER recall target: **≥ 0.85** (recall slack goes into the review queue)

---

## 6. Retrieval & Reasoning

### 6.1 Multi-strategy retrieval

The agent chooses among six retrieval strategies per sub-query:

| Strategy | Engine | Best for |
|---|---|---|
| Direct entity lookup | Cypher on FalkorDB | Known identifiers |
| Semantic text search | pgvector + pgvectorscale (StreamingDiskANN) + jina-embeddings-v3 | Fuzzy description match |
| Image similarity | pgvector + OpenCLIP embeddings | Reverse-image / face / scene |
| k-hop graph walk | FalkorDB weighted paths | "How are X and Y connected?" |
| Community retrieval | Leiden clustering + LLM community summaries | "What's the broader network?" |
| Bitemporal query | Graphiti | "Were X and Y both at Z in 2024?" |

Results from all engaged strategies are fused via **Reciprocal Rank Fusion**, then reranked with **ColBERTv2** and **Cohere Rerank 3** (fallback: `jina-reranker-v2`).

### 6.2 Composite reasoning: HippoRAG 2 + PathRAG + OG-RAG

We compose three SOTA retrieval-augmentation patterns that together dominate single-pattern approaches. No competitor ships this combination.

**HippoRAG 2** — substrate
- Build a document→entity bipartite layer atop the FalkorDB graph.
- Personalized PageRank from query-seed entities weighted by embedding similarity.
- Retrieve evidence from highest-scoring nodes' associated documents.
- Baseline: ~87-91% evidence recall, 10-30× cheaper multi-hop than vanilla GraphRAG.

**PathRAG** — path pruning
- After `bounded_pathfind` or HippoRAG returns candidate paths, apply flow-based pruning (PathRAG algorithm) to keep only information-bearing paths.
- Cuts context tokens by ~44% without accuracy loss.
- Directly improves our LLM cost per investigation (Claude Advisor token spend is the largest variable cost).

**OG-RAG** — ontology grounding
- Our typed entity + edge ontology (§4.1, §4.2) is already schema-constrained.
- Apply OG-RAG's schema-constrained extraction during retrieval and synthesis — LLM outputs that don't validate against the ontology are rejected/retried.
- Reduces hallucinations ~40% on structured domains.

### 6.3 Why this wins

Benchmark published results (SOTA April 2026):
- Vanilla RAG multi-hop recall: ~40-55%
- GraphRAG multi-hop recall: ~70-80%
- HippoRAG 2 multi-hop recall: **87-91%** at **10-30× lower cost** than GraphRAG
- **Our composite target: ≥ 90% recall at ~50% of HippoRAG 2 context tokens** (via PathRAG pruning) with ~40% fewer hallucinations (via OG-RAG schema gating). This is the retrieval contract we hold ourselves to, measured on MuSiQue + 2WikiMultiHop + our own OSINT benchmark harness (§12).
- Production baseline for enterprise RAG is still hybrid (~65-75%). We're choosing to skate ahead.

---

## 7. The Connection-Finding Engine (Core Differentiator)

Ten typed MCP tools that compose into investigation strategies no competitor exposes.

### 7.1 `generate_hypotheses(seed_a, seed_b)`
LLM proposes ranked connection hypotheses ("shared address," "same phone in different breaches," "temporal coincidence at location," "shell-company control chain"). Output: list of hypothesis objects with testable predicates.

### 7.2 `bounded_pathfind(seed_a, seed_b, max_hops=4, edge_filters, min_confidence=0.6)`
Weighted shortest-paths over FalkorDB; weights = `-log(edge_confidence)` so shortest path = maximum-likelihood path. Returns up to N paths with per-edge citations.

### 7.3 `probabilistic_path_score(path)`
Product of edge confidences with Bayesian smoothing on prior evidence density. Filters chain-of-Chinese-whispers noise.

### 7.4 `community_co_membership(seed_a, seed_b, threshold=0.7)`
Leiden clustering at multiple resolutions; returns communities containing both seeds with density and overlap scores. Catches "same network, no direct edge."

### 7.5 `temporal_coincidence(entities[], location_radius_m, time_window)`
Bitemporal query via Graphiti: did any subset of these entities share a location within the radius/window? Returns ranked coincidence events with evidence.

### 7.6 `embedding_proximity_without_edges(seed, k=20)`
Top-k semantically similar entities with **zero graph path** within 3 hops. Surfaces likely aliases, sock puppets, shell identities.

### 7.7 `structural_role_mining(entity)`
GraphSAGE-style structural embeddings. Detects "nexus nodes" (addresses with many LLCs, emails in many breaches, phones across many accounts). Output: role-similarity to known patterns (e.g., "looks like a mail-drop address," "looks like a burner email").

### 7.8 `contradiction_detect(entity)`
Bitemporal reasoning: identifies overlapping-valid-time edges with contradictory values from distinct sources. Ranks by significance (recency, source credibility, magnitude of discrepancy).

### 7.9 `predict_missing_link(entity, relation_type, top_k=10)`
Knowledge-graph-embedding-based link prediction (RotatE + hybrid semantic-structural model, with TGN temporal augmentation in Phase 2). Given an entity and a target relation type, ranks the most probable targets that *should* have that edge based on graph structure and semantics. Used for **proactive lead generation** — e.g., "given what we know about John Smith, which phone numbers in the graph are most likely his?" Returns ranked candidates with confidence and supporting structural features.

### 7.10 `cross_source_corroboration(claim, min_sources=3)`
Given any assertion (from a tool, the user, or another primitive), triangulates supporting and contradicting evidence across all indexed sources. Sources are weighted by a credibility tier (tier S: official records / court / regulatory; tier A: major media / industry registry; tier B: social / commercial APIs; tier C: self-reported / user-contributed). Returns a Bayesian-smoothed confidence score and a citation pack. **Journalist-grade verification primitive** — this is what goes into the evidence bundle for court or publication.

### 7.11 Composition

The agent composes these primitives. Example investigation template **"Are these two people the same?"**:
1. `structural_role_mining(A)` + `structural_role_mining(B)` — pattern similarity
2. `embedding_proximity_without_edges(A)` → does B appear?
3. `bounded_pathfind(A, B, max_hops=3)` — direct connections
4. `community_co_membership(A, B)` — shared network
5. `contradiction_detect` on each side — lies/inconsistencies
6. `cross_source_corroboration` on the "same person" claim — evidence triangulation
7. LLM synthesizes a ranked verdict with evidence

Example **"What else should we look at?"**:
1. `predict_missing_link(seed, relation_type=*)` → top-k predicted edges per relation
2. Filter by high structural confidence + no existing edge
3. Surface as "leads the agent thinks are worth investigating" panel

---

## 8. Agent Orchestration

### 8.1 LangGraph state machine (orchestration)

States: `Plan → Collect → Resolve → Traverse → Hypothesize → Verify → Synthesize → Report`.

Each state has a Claude Advisor invocation (Opus 4.7 advisor + Haiku 4.5 executor) and emits typed transitions. The whole run is replayable from the event log.

### 8.2 DSPy module per node (prompt optimization)

Each LangGraph node's prompt logic is implemented as a **DSPy module**. Rationale (SOTA pattern as of 2026):
- DSPy compiles prompts against labeled eval traces, delivering 99%+ reliability on each reasoning step.
- Cross-model portability: the same LangGraph pipeline can run on Claude, GPT-5.4, or Llama 4 with zero prompt rewrites — important for BYOK customers and future pricing leverage.
- Upfront compilation cost (100-500 LLM calls per module) is paid once per release; amortized over millions of investigations.

### 8.3 Claude Advisor Tool

Advisor guidance is solicited at each plan/pivot checkpoint (high-value, low-frequency); Haiku executes routine work (tool dispatch, extraction, normalization). Expected per-investigation LLM cost: **$0.30-$2.50** depending on depth, down from ~$8-15 in a pure-Opus setup.

### 8.4 BAML typed outputs

Every LLM call returns a typed, validated object via BAML. No JSON-parsing roulette, no hallucinated fields. BAML schemas are the interface contract between the agent and the graph-writer. BAML schemas are also the inputs to DSPy's signature system — the two toolchains compose cleanly.

### 8.5 Audit trail

Every state transition, tool call, retrieval, and LLM output is written to the investigation's event log in Postgres + traced via OpenTelemetry. The final "Report" includes a collapsible reasoning trace with citations.

---

## 9. Tool Catalog

Organized by investigation phase. v1 targets **36 tools**; Phase 1.5 adds **4**; Phase 2 adds **~15**.

### v1 — Phase 1 (36 tools)

#### Domain / Infrastructure (8)
1. `subfinder_passive` (PD Go lib)
2. `dns_lookup_comprehensive` (dnsx lib)
3. `whois_historical` (WhoisXML API)
4. `cert_transparency_query` (crt.sh API)
5. `asn_lookup` (Team Cymru + BGPView)
6. `reverse_dns` (ptr records + historical)
7. `domain_age_history` (SecurityTrails)
8. `http_probe` (httpx lib — tech fingerprint, screenshot)

#### Stealth HTTP layer (1 — foundational)
9. `stealth_http_fetch` — JA4+ / TLS-fingerprint-matching HTTP client via `rnet` (Python). Impersonates real-browser TLS handshakes. Hits ~30-40% of Cloudflare/DataDome-protected sites at browser-free cost. Used internally by most other tools; also exposed directly for advanced users.

#### Internet-connected devices / hosts (3)
10. `shodan_search` (Shodan API)
11. `censys_search` (Censys API)
12. `port_scan_passive` (naabu lib, safe-mode)

#### Person / Identity (6)
13. `username_sherlock` (custom Go port of sherlock logic)
14. `email_holehe` (Python worker)
15. `email_breach_lookup` (HIBP + DeHashed)
16. `phone_numverify` (NumVerify + libphonenumber)
17. `google_account_ghunt` (Python worker)
18. `people_public_records` (per-jurisdiction adapters)

#### Social media (5)
19. `instagram_public_profile` (instaloader-scoped)
20. `twitter_snscrape_public` (snscrape)
21. `linkedin_public_profile` (compliant commercial API partner)
22. `reddit_api_query` (official Reddit API)
23. `github_user_profile` (GitHub API)

#### Leaks / Breach intelligence (3)
24. `hibp_lookup` (HIBP v3)
25. `intelx_search` (Intelligence X, commercial)
26. `dehashed_search` (DeHashed, commercial)

#### Corporate records + sanctions (3 — new in v1)
27. `opencorporates_search` (OpenCorporates API — 200M+ companies, 120+ jurisdictions)
28. `opensanctions_screen` (OpenSanctions — sanctions lists, PEPs, cross-links to OpenCorporates)
29. `sec_edgar_filing_search` (SEC EDGAR — US public filings, historical)

#### Content / Archives (3)
30. `wayback_history` (Wayback API)
31. `common_crawl_lookup` (Common Crawl index)
32. `pdf_document_analyze` (extract text + entities + exif)

#### Geospatial / Image (2)
33. `reverse_image_search` (multi-backend: Yandex + TinEye + Bing Visual)
34. `exif_extract_geolocate` (exiftool + geocoder)

#### Meta (2)
35. `tool_cost_estimate` — returns credit-cost estimate for a proposed tool-call plan before execution (helps the agent stay within user budget)
36. `evidence_bundle_export` — packages case state into immutable evidence bundle (Merkle-tree rollup, signed manifest) for court/publication

### Phase 1.5 additions (months 3-5)

37. `gdelt_gkg_query` — GDELT Global Knowledge Graph 2.0 via BigQuery. Subjects across global media in 100+ languages, updated every 15min. Transformative for journalists.
38. `deepfake_detect_media` — Reality Defender API for image/video/audio authenticity. Essential journalist tooling.
39. `gdelt_visual_gkg_query` — GDELT's Visual GKG for image-in-news analysis.
40. `hive_moderate_batch` — Hive AI for high-throughput content authenticity (team-tier feature).

### Phase 2 additions (~15 tools)

- **Audio OSINT:** `deepgram_transcribe_diarize` (ASR + speaker diarization), `deepgram_voice_clone_detect`, `whisper_transcribe_local` (BYOK tier)
- **Face / identity verification:** `face_search_multi_backend` (FaceCheck.ID + Lenso.ai + Yandex, consent-gated with AUP enforcement)
- **Crypto tracing:** `arkham_entity_lookup`, `arkham_fund_flow`, `breadcrumbs_bridge_trace`, `breadcrumbs_mixer_detect`, `etherscan_tx_history`, `solscan_tx_history`
- **Dark web (partner):** `darkowl_search` (via DarkOwl API partnership)
- **Advanced infra:** `silentpush_passive_dns`, `silentpush_iofa_feed`, `domaintools_iris_pivot`
- **Corporate deep:** `companies_house_uk_search`, `dun_bradstreet_lookup` (premium only)
- **Sanctions/adverse media:** `adverse_media_search` (aggregator across GDELT + major registries)
- **Integrations:** `hunchly_sync` (read-only import of user's Hunchly cases), `maltego_transform_bridge` (export to Maltego graph format)

### Phase 3 (deferred)

- PimEyes deep face (if commercial access terms become tractable)
- Chainalysis Reactor (only if enterprise tier launches)
- Voice biometric matching at scale
- Satellite imagery adapters (Sentinel Hub, Planet Labs)
- Global court-records API (PACER, state courts) where feasible

---

## 10. Infrastructure & Deployment

### 10.0 Scraping hierarchy (four-tier stealth ladder)

Each tool-level scrape attempt walks this ladder, stopping at the first tier that succeeds. This drives 80%+ of the infrastructure cost savings vs competitors who default to headless browsers.

| Tier | Tech | Cost / request | Hits | When used |
|---|---|---|---|---|
| 1 | **Direct HTTP** (Go stdlib, Python `httpx`) | ~$0 | Unprotected / public APIs | Default for most OSINT data sources |
| 2 | **Stealth HTTP** (`rnet` with JA4+ impersonation) | ~$0 + proxy cost | ~30-40% of Cloudflare / DataDome sites | When Tier 1 is blocked at WAF/TLS layer |
| 3 | **Playwright / Nodriver** (headless Chrome with CDP-direct + stealth patches) | ~$0.002 (compute) | ~50-70% of JS-heavy sites | When Tier 2 fails (CAPTCHA, behavioral) |
| 4 | **Camoufox** (Firefox antidetect, scores 0% on CreepJS) | ~$0.005 (compute) + premium residential proxy | Near-100% of DataDome / Turnstile / Akamai-protected sites | Last resort — heavy anti-bot |

### 10.1 Runtime

- **Fly.io** primary for multi-region workers. Scrapers in EU, NA, APAC to avoid geo-fenced bot detection and reduce cross-region latency.
- **GCP** (existing account) for heavy batch / training of Splink models / ClickHouse analytics (if needed).
- **Cloudflare** for DNS, R2, WAF, Turnstile.

### 10.2 Proxy / data-plane

- **BrightData residential** (premium-target scraping; metered per-tenant)
- **Oxylabs datacenter** (bulk)
- **Owned Hetzner DC rotating pool** (~€3/IP/mo, low-risk work)
- **Tor** (passive-only, read-only queries)
- **Key rotation pool**: 3–5 accounts per paid data source, rotated per-tenant

### 10.3 Data-plane caching

Every external API response is cached to R2 (by normalized-query hash) with a source-specific TTL. Cache hits incur no third-party API cost. Cross-tenant cache sharing applies **only to deterministic public-data sources** (WHOIS, DNS, cert transparency, public ASN data, Wayback, Common Crawl) where the result is a property of the queried identifier rather than a function of the user. Commercial-dataset responses (HIBP, Shodan, DeHashed, etc.) and any user-specific reasoning outputs are never cross-tenant-cached. Default-on for eligible sources with clear disclosure in the privacy policy; opt-out available per-tenant.

### 10.4 Observability

- **OpenTelemetry** traces/logs/metrics → Grafana Cloud
- **Sentry** errors
- **ClickHouse** (optional Phase 1.5) for tool-usage analytics / billing meters / product insights

### 10.5 Security

- mTLS between services
- All inter-service calls signed (Ed25519)
- Postgres RLS for tenant isolation
- FalkorDB multi-tenancy via graph-per-tenant; tenant ID in every query
- R2 artifact paths include tenant ID + SHA-256; accessed via short-lived signed URLs
- Secrets via Infisical or Google Secret Manager
- SOC 2 Type I target: 12 months. Type II: 24 months.

### 10.6 Cost model (rough v1 unit economics)

| Item | Per-month at 1K users | Per-month at 10K users |
|---|---|---|
| Fly.io compute | $800 | $5,000 |
| Postgres managed | $400 | $2,500 |
| FalkorDB | $300 | $1,500 |
| DragonflyDB | $200 | $800 |
| R2 storage + egress | $150 | $900 |
| Proxy pool | $2,000 | $12,000 |
| Third-party API licenses | $3,000 | $18,000 |
| LLM (Claude Advisor) | $1,500 | $12,000 |
| **Total infra** | **~$8,350** | **~$52,700** |
| **Revenue (avg $80/user)** | **$80,000** | **$800,000** |
| **Gross margin target** | **~90%** | **~93%** |

Cache-hit rate compounding drives margin — at steady-state 60–80% hit rate on the expensive APIs, per-investigation variable cost trends toward zero.

---

## 11. Non-Functional Requirements

| Category | Target |
|---|---|
| MCP tool call p95 latency (cached) | < 200ms |
| MCP tool call p95 latency (uncached) | < 4s |
| Multi-hop connection-find p95 | < 8s (up to 4 hops, per-tenant graph ≤ 10M nodes) |
| Investigation end-to-end (medium complexity) | 5–15 minutes |
| Uptime | 99.9% |
| Tenant isolation | Cryptographic (RLS + graph namespacing + signed artifact URLs) |
| Data retention | Tier-dependent (30/180/365 days); user-triggered deletion within 24h |
| DSAR response | < 30 days (GDPR) |
| Evidence chain immutability | R2 object-lock + Merkle-tree rollups |

---

## 12. Phasing & Milestones

### Phase 1 — MVP (target: 4 months, private beta)
- MCP server (stdio + Streamable HTTP) with 25 of the 36 v1 tools (incl. stealth HTTP layer, corporate records, sanctions)
- **Three-tier ER** (GLiNER + Jellyfish-8B self-hosted + Claude Haiku 4.5)
- **FalkorDB + Graphiti** graph layer with bitemporal writes
- **6 of 10 connection primitives:** `bounded_pathfind`, `probabilistic_path_score`, `generate_hypotheses`, `temporal_coincidence`, `community_co_membership`, `cross_source_corroboration`
- **Composite retrieval:** HippoRAG 2 baseline + OG-RAG schema gating
- LangGraph + BAML orchestration (DSPy compilation in Phase 2)
- Postgres + auth + billing (Stripe) + credit metering
- OSINT benchmark harness v1 (MuSiQue + 2WikiMultiHop + 50 curated real-world cases)
- **Launch invite-only beta (target: 100 users)**

### Phase 1.5 — Public Beta (months 4-6)
- Remaining 11 v1 tools (full 36-tool catalog)
- **4 connection primitives added:** `embedding_proximity_without_edges`, `structural_role_mining`, `contradiction_detect`, `predict_missing_link` (RotatE-based)
- **PathRAG** context pruning integrated into retrieval
- **DSPy** module compilation for core prompts
- **GDELT + Reality Defender + Hive** tools (Phase 1.5 additions)
- Web analyst UI (SvelteKit — Phase 2 preview behind feature flag)
- SOC 2 Type I audit commences

### Phase 2 — GA (months 6-10)
- **Multi-region workers** deployed (Fly.io EU + APAC)
- **BYOK flow** (Advanced + Team tiers)
- Crypto tracing, audio OSINT, face search, dark web (DarkOwl partnership), Silent Push
- Team accounts + RBAC
- **Evidence bundle export** (court-grade Merkle-rolled PDF + manifest)
- **TGN temporal link prediction** upgrade to `structural_role_mining` and `predict_missing_link`
- Hunchly integration, Maltego bridge export
- SOC 2 Type I achieved
- **Public launch**

### Phase 3 — Scale (months 10-18)
- Graph foundation model evaluation (GraphGPT / OpenGraph) for next-gen link prediction
- Real-time monitoring / alerting (watched-entity change detection)
- Advanced corporate adapters (Companies House, Dun & Bradstreet premium)
- Satellite imagery (Sentinel Hub, Planet Labs)
- Voice biometric matching at scale
- Marketplace for user-contributed tool adapters
- SOC 2 Type II, ISO 27001 optional
- White-label / reseller tier evaluation

---

## 13. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Legal action from scraped platforms | Medium | High | Conservative scraping posture; platform-compliant adapters where available; explicit AUP; legal counsel on retainer |
| Breach / data leak of customer cases | Low | Catastrophic | Tenant isolation, RLS, encryption at rest/in flight, SOC 2, bug bounty |
| LLM cost inflation | Medium | Medium | Advisor Tool pattern caps cost; prompt caching; Haiku-first routing; per-tier credit caps |
| Third-party API price shocks | Medium | Medium | Multi-source redundancy per data type; cache amortization; contracts with annual rate locks |
| Competitors ship AI-native first | Medium | High | Bitemporal + connection primitives + pricing are the moat, not tool count; ship connection primitives early as the differentiator |
| Abuse (stalking, doxing) | Medium | Catastrophic (reputational) | AUP + anomaly detection on usage patterns + human review on red-flag queries (e.g., repeated single-individual targeting) + account suspension |
| FalkorDB/Graphiti project health | Low | High | Graph layer behind a thin abstraction; Neo4j fallback implementation kept in CI |

---

## 14. Out of Scope (v1)

- Law enforcement / government customers (separate compliance program required)
- On-prem / air-gapped deployment
- Mobile apps
- Non-English entity extraction beyond top-10 languages (GLiNER covers the top 10 natively)
- Voice / audio OSINT (speaker ID, voiceprint)
- Video content analysis beyond exif + reverse-image on keyframes
- Real-time monitoring / alerts (Phase 3)
- Marketplace for user-contributed tools (Phase 3)

---

## 15. Open Questions

1. **Branding / name** — `osint-agent` is a placeholder. Candidates to consider: Argus, Meridian, Telltale, Beacon, Nexus, Clue.
2. **Initial LLM provider lock-in** — default Claude (recommended), but plan for an abstraction layer to allow GPT-5.4 / Gemini 2 Pro for cost-sensitive workloads or BYOK customers.
3. **Open-source strategy** — should we open-source the tool-worker layer and/or the connection primitives as lead-gen? (Classic OSS-as-marketing play for developer-adjacent products.) Recommendation: open-source the tool adapters (Apache 2.0), keep the ER pipeline + connection primitives + graph schemas closed.
4. **Reseller / white-label tier** — demand likely exists; defer decision to post-GA.

---

## 16. Appendices

### Appendix A — Reference reading (external)
- HippoRAG 2 multi-hop RAG pattern
- PathRAG flow-based path pruning
- OG-RAG ontology-grounded extraction
- FalkorDB GraphBLAS architecture
- Graphiti bitemporal KG design
- Splink Fellegi-Sunter ER
- Jellyfish-8B / Jellyfish-13B data preprocessing LLM
- Claude Advisor Tool documentation
- MCP spec v2.1 (Server Cards)
- GLiNER-Relex extraction pipeline
- RotatE / ComplEx knowledge graph embedding for link prediction
- TGN / TGN-SEAL temporal graph networks
- ProjectDiscovery tool suite (subfinder, httpx, dnsx, naabu, katana)
- JA4+ TLS fingerprinting (FoxIO)
- `rnet` / `curl_cffi` / `pyreqwest-impersonate` HTTP impersonation libraries
- GDELT Global Knowledge Graph 2.0 + Visual GKG
- OpenSanctions + OpenCorporates + Nomenklatura toolkit
- LangGraph + DSPy + BAML composition pattern (2026 production stack)

### Appendix B — Glossary
- **Bitemporal**: Modeling both valid-time (when true in the world) and system-time (when recorded).
- **Entity Resolution (ER)**: Determining that two records refer to the same real-world entity.
- **Fellegi-Sunter**: Probabilistic record linkage framework (1969) that Splink implements.
- **HippoRAG**: Neurobiologically-inspired retrieval using personalized PageRank.
- **JA4+**: Family of TLS/HTTP fingerprints (FoxIO, 2023) that replaced JA3 after Chrome 110 randomized extension order. Universal industry standard for bot detection as of 2026.
- **KGE**: Knowledge-graph embedding (RotatE, ComplEx, etc.) — vector representation of entities and relations used for link prediction.
- **Leiden**: Community detection algorithm, improvement over Louvain.
- **MCP**: Model Context Protocol — standard interface between LLMs and external tools.
- **OG-RAG**: Ontology-grounded RAG — schema-constrained extraction to reduce hallucinations.
- **PathRAG**: Flow-based path pruning for graph RAG, reduces context tokens ~44%.
- **Reciprocal Rank Fusion (RRF)**: Method for combining ranked lists from different retrieval systems.
- **TGN**: Temporal Graph Network — neural model for dynamic graphs.
