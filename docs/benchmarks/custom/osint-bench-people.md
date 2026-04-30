# OSINT-Bench-People

**Family:** `osint-bench-people` · **Status:** Methodology complete; corpus construction is ethics-gated.

## What it measures

Quality of an identity dossier built from a single seed (name / email / username). Modeled on Trace Labs Search Party CTF flag categories — this is the only battle-tested OSINT scoring rubric in the field.

## Why we built it

The competitive landscape is empty:

- Trace Labs' rubric is the gold standard but their datasets are real missing-persons cases — private, time-bounded, can't be redistributed.
- Username-tool comparisons (Sherlock vs. Maigret) measure raw matching, not investigation quality.
- Bellingcat is reportedly building an LLM-driven OSINT tool with eval expected April 2026, but no public benchmark yet.

A public people-OSINT benchmark with a real leaderboard does not exist. Building one is a Schelling-point play.

## Ethics gate (mandatory before corpus construction)

Subjects in the corpus must be:
- **Public figures** (politicians, journalists, OSINT-Twitter educators, academics) **OR**
- **Explicit-consent volunteers** who have signed a written agreement covering the specific findings categories.

No private individuals. No domestic-violence-risk profiles. No active-investigation overlap. The corpus card includes the consent record per subject. This rule is non-negotiable and any PR adding a subject without consent is auto-rejected.

## Methodology

For each subject, the seed is **one of**: real name, primary email, primary username, or primary domain. The agent has nothing else.

The agent runs `person_aggregate` and any tools it judges relevant. Output is scored against a hand-graded rubric mirroring Trace Labs flag categories:

| Category | Per-finding points | Scoring rule |
|---|---|---|
| Email address (verified) | 50 | LLM-judge confirms it's the subject's, not a namesake |
| Phone number (verified) | 50 | Same |
| Personal social profile | 30 | Per-platform; platform must be verified to belong to subject |
| Professional profile | 20 | Same |
| Friend / Family identification | 50 | Must include relevance evidence (Trace Labs rule) |
| Last known location | 100 | Time-bounded, evidence-linked |
| Vehicle / Asset identification | 75 | Photo + plate / license / public registry link |
| Employer (current) | 30 | |
| Education history | 20 | |
| Cross-platform handle correlation | 25 | Same handle on ≥3 platforms with evidence of same person |
| Hidden-but-derivable findings | 200 | E.g., personal email derivable only via cross-pivot |
| **Penalties (negative score)** | | |
| Wrong-person finding | -100 | Confused subject with a namesake |
| Hallucinated finding | -200 | Cited a source that doesn't say what was claimed |

Scoring is performed by an LLM judge (Claude Opus, no agent loop) given the original web sources cited by the agent. Wrong-person and hallucination penalties are aggressive on purpose — over-confident agents are the failure mode we most want to discourage.

## Headline competitive context

- Trace Labs publishes per-CTF totals but not per-tool-stack scores; there's nothing to compete with directly.
- Sherlock-class tools score ~0 on this benchmark because they can't justify findings.

We publish quarterly. Anyone can submit a tool-stack to be benchmarked against the public corpus.

## What "winning" looks like

- **Floor:** positive score (i.e., real findings outweigh hallucinations). An astonishing fraction of generic LLM agents fail this floor today.
- **Stretch:** outperform a 30-minute manual investigation by a Trace-Labs-trained human (we'll baseline this with 5 volunteers).
- **Aspirational:** consistently surface the "Hidden-but-derivable" 200-point findings.

## Sources

- [Trace Labs Flag Categories Guide](https://github.com/C3n7ral051nt4g3ncy/TraceLabs-Flag-Categories-Guide)
- [Trace Labs Search Party rules](https://www.tracelabs.org/about/search-party-rules)
