# OSINT-Bench-Adversary

**Family:** `osint-bench-adversary` · **Status:** Methodology drafted; corpus construction in progress.

## What it measures

Detection rate on infrastructure that someone is *actively trying to hide*. This is the benchmark we want to own — no commercial vendor benchmarks themselves on adversarial OSINT, because their products are tuned for clean asset discovery, not adversary tradecraft.

This benchmark directly maps to the proprietary moat described in `docs/specs/2026-04-22-osint-agent-design.md`: World Model + Adversary Library + Predictive Temporal Layer. If we can't move this benchmark, the moat doesn't exist.

## Categories

Six categories, each with a curated corpus of "needle in haystack" cases:

1. **Typosquats / lookalike domains** (`typosquat_scan`)
   - Seed: a brand domain. Ground truth: known active typosquat domains targeting it (PhishTank, OpenPhish corpus filtered to the brand).
   - Score: recall against known-bad list.
2. **Subdomain takeovers** (dangling DNS) (`takeover_check`)
   - Seed: a domain with a curated set of intentionally-dangling subdomains in a controlled lab. Ground truth: which subdomains are takeoverable.
   - Score: recall and FP rate (false positives = "nuke production" outcomes).
3. **Leaked secrets in public repos** (`leaked_secret_git_scan`, `git-secrets`)
   - Seed: an org's GitHub org name. Ground truth: a curated set of historical real leaks (truffleHog corpus, sanitized).
   - Score: recall against the known-leak list, with severity-weighting.
4. **Fast-flux / DGA infrastructure** (`asn_lookup`, `urlscan_search`, `favicon_pivot`)
   - Seed: one IP from a known fast-flux botnet (historical, sanctioned-by-takedown). Ground truth: the cluster.
   - Score: cluster-recall (% of cluster nodes correctly identified within 2 pivot hops).
5. **Hidden-by-default identity correlation**
   - Seed: a username known to use different handles across platforms. Ground truth: the cross-platform map (consenting public figures only).
   - Score: % of true cross-platform handles correctly linked.
6. **Source-rot resistance** (the temporal differentiator)
   - **Why it's here:** discovered empirically on 2026-04-29 — `amass@v4` passive-source coverage on `tesla.com` collapsed ~82% (342 → 62 candidates) vs the Black Lantern 2022 reference. Free OSINT sources rot fast. Tools differ wildly in how well they detect rot, route around it, and pick up new sources. Most published benchmarks (including BLS 2022, which the open web still cites in 2026) ignore this entirely. **No commercial vendor benchmarks themselves on freshness** — this category is uncontested.
   - Methodology: pin a corpus snapshot at time T0 (subjects, ground truth). Re-run the same suite at T0+12 weeks, T0+26 weeks, T0+52 weeks. Each re-run produces a per-tool freshness delta.
   - Score components:
     - **Δ-recall:** how much did the tool's recall move on the same corpus? Stable score = good.
     - **Source-attribution recall:** when a source dies (e.g. an API gets paywalled), does the tool *report it* in its own telemetry, or does it silently degrade? Tools that self-document degradation score higher.
     - **New-source pickup latency:** when a new free source appears (e.g. a new CT log, a new IoC feed), how many weeks until the tool integrates it? Measured by injecting synthetic findings into a controlled new-source mock and timing detection.
   - Headline metric: rolling-12-week macro-F1 stability. A tool whose F1 drops from 0.99 → 0.40 over a year scores 0.40 here even though its "today" F1 might still look good in a static benchmark.
   - **This is the category the proprietary World Model + Predictive Temporal Layer should dominate.** Predicting source rot before it happens (and pre-pivoting) is the design intent in `docs/specs/2026-04-22-osint-agent-design.md` §4.x. Without OSINT-Bench-Freshness, that capability is unmeasurable; with it, the moat becomes a number.

## Methodology

- **Single overall score** = macro-average F1 across the 5 categories. Per-category scores are kept for diagnostic use.
- **Adversary update cadence:** the corpus rotates quarterly. Two-week disclosure window before publication so vendors can verify they're not being unfairly graded against takedowns that happened this morning.
- **Open submission:** any tool-stack can submit. We publish the scoring code; we keep the ground-truth labels hidden until the submission window closes.

## Headline competitive context

There is genuinely no comparable benchmark in commercial security. CyCognito's "seedless discovery" claim is the closest marketing equivalent, but they don't publish detection rates against an adversarial corpus.

## What "winning" looks like

- **Floor:** category recall > 0 in all five categories. Many tool-stacks fail entirely on category 4 (fast-flux clustering) and category 5 (hidden identity correlation).
- **Stretch:** macro-F1 ≥ 0.50. That would be a publishable industry-first.
- **Aspirational:** macro-F1 ≥ 0.70 with the future Adversary Library wired in.

## Why this is the moat

- The 5 categories require *cross-tool reasoning*, not single-tool excellence. A single-source ASM vendor can't compete on category 4 or 5 even if they have great asset discovery.
- The corpus is hard to build, expensive to maintain, and ethically gated. First mover with a credible corpus locks the comparison frame for years.
- We control the scoring rubric. Future versions can reward "predictive temporal" detections (catching the typosquat before the phishing email arrives), which only `osint-agent`'s design ladders to.

## Open methodology risks

- **Disclosure timing:** publishing a corpus of takeoverable subdomains (cat 2) means weaponizing them. Cat 2 ground truth lives in a controlled lab with our own dangling records, not real-world ones.
- **Ground-truth rot:** typosquat domains get taken down. We snapshot DNS/WHOIS state at corpus-pin time and grade against the historical state.

## Sources for ground-truth feeds

- [PhishTank](https://www.phishtank.com/) — phishing URL feed
- [OpenPhish](https://openphish.com/) — phishing URL feed
- truffleHog historical leak corpus
- [DNStwist](https://github.com/elceef/dnstwist) — typosquat permutation reference
- Spamhaus / Abuse.ch — fast-flux historical clusters
