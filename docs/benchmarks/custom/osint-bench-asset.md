# OSINT-Bench-Asset

**Family:** `osint-bench-asset` · **Status:** In development. Phase 1 corpus = 10 organizations.

## What it measures

Recall and precision of full asset discovery (subdomains + IPs + ASNs + certs + tech-stack) against a published, human-curated ground-truth scope.

## Why we built it

Commercial ASM vendors (CyCognito, Bitsight, Tenable, Wiz) all market themselves on coverage but never publish head-to-head benchmarks against a fixed dataset. The closest public datapoint is a vendor-curated case study or a BBOT/Subfinder/Amass face-off (which only covers subdomains).

We benchmark against **publicly disclosed bug-bounty scopes** from organizations that have explicitly enumerated their attack surface as part of their program. Those are real, ground-truthed asset inventories made public.

## Phase 1 corpus

Ten orgs with a published "in-scope" list on their bug-bounty program. Each org contributes ~50–500 ground-truth assets across subdomains, IP ranges, mobile apps, and APIs:

| Org | Source | Scope size (approx) |
|---|---|---|
| GitHub | hackerone.com/github (security.txt cross-ref) | 200+ |
| GitLab | hackerone.com/gitlab | 100+ |
| Cloudflare | hackerone.com/cloudflare | 300+ |
| HackerOne (themselves) | hackerone.com/security | 80+ |
| Atlassian | bugcrowd.com/atlassian | 200+ |
| Mozilla | bugzilla.mozilla.org/security | 150+ |
| Yelp | hackerone.com/yelp | 100+ |
| Reddit | hackerone.com/reddit | 80+ |
| US DoD (`*.mil` subset) | hackerone.com/deptofdefense — public-facing scope only | varies |
| Coinbase | hackerone.com/coinbase | 150+ |

Final list is pinned in `apps/api/test/benchmarks/asset-corpus-v1.json` with a content hash.

## Methodology

For each org:
1. Seed input = the org's primary domain only. The agent has nothing else to start from — the test is whether it can fan out to the published scope.
2. Run `domain_aggregate` end-to-end. Capture every asset emitted.
3. Score `{recall, precision, f1}` against the published scope.

For corpus-level numbers we report **macro-averaged F1** across the 10 orgs (so a single big org doesn't dominate). Per-org F1 is also kept for diagnostic use.

## Headline competitive context

There is no published recall number from any commercial ASM vendor against any of these scopes. Period. If we publish a credible 50%+ macro-F1, that becomes the ASM industry's first apples-to-apples public benchmark — which, by itself, is a marketing event.

## What "winning" looks like

- **Floor:** macro-F1 ≥ 0.40 — equivalent to a competent solo subfinder run with light enrichment.
- **Stretch:** macro-F1 ≥ 0.65 — close to what a manual recon-engineer day produces.
- **Aspirational:** macro-F1 ≥ 0.80 with adversary-tier additions (cf. OSINT-Bench-Adversary).

## Open methodology risks

- **Scope creep:** Bug-bounty scopes evolve; we pin a snapshot date and refresh quarterly.
- **Out-of-scope in-the-wild assets:** The org may own assets they didn't list. We score against the published list only — discovery of unlisted-but-real assets shows up in `score_breakdown.fp_potential_real` for diagnostic use, never in the headline F1.
- **Right-to-test:** Bug-bounty in-scope assets are explicitly opted-in for active testing. Our `domain_aggregate` is passive only on this benchmark by default.
