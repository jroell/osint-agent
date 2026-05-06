// Identity confidence scoring (iter-22).
//
// Maps a deduped identity (post iter-17 canonical-key dedup) to a
// normalized 0.0-1.0 confidence score plus a tier label. The score
// is built from independent verifiability signals so the
// "is this real?" question downstream of person_aggregate has a
// single typed answer instead of a hand-rolled heuristic per caller.
//
// Signal sources (each contributes at most a fixed weight; the final
// score is capped at 1.0):
//
//   1. Source diversity (max 0.40): N independent tools surfacing
//      the same canonical key. A single tool getting hits is weak;
//      4+ independent tools reaching the same node is strong evidence
//      it's a real public profile, not a hallucinated link.
//
//   2. Cryptographic verification (max 0.50): Keybase identity
//      proofs are signed and chain back to a public key. Anything
//      flagged verified=true gets the full 0.50 — there's no stronger
//      signal in OSINT for "this account belongs to this person."
//
//   3. URL canonicalization (max 0.15): a deterministic canonical
//      profile URL means the platform's URL contract is satisfied
//      (recognized platform, recognized path shape). Adds a floor
//      under the score for known-good-shape identities.
//
//   4. Display-name presence (max 0.10): a non-empty display_name
//      means at least one tool fetched the profile and got real
//      metadata, not just a dangling reference URL.
//
//   5. Multi-platform corroboration (max 0.05): if the same handle
//      appears on ≥2 platforms in the same person_aggregate result,
//      that's weak but real evidence of a unified online identity.
//      This is computed at the aggregate level, not per-identity.
//
// The tiers map to actionable next steps:
//   - "single-source"      score < 0.40: treat as a lead, not evidence
//   - "corroborated"       0.40-0.69:    cite as evidence, flag as needs-more
//   - "strongly-verified"  0.70-0.89:    cite freely, downstream graph build
//   - "cryptographic"      ≥ 0.90:       court-/journalism-grade citation

import type { DedupedIdentity } from "./aggregate-dedup";

export type IdentityConfidence = {
  score: number;
  tier: "single-source" | "corroborated" | "strongly-verified" | "cryptographic";
  reasons: string[];
};

export type ScoredIdentity = DedupedIdentity & {
  confidence: number;
  tier: IdentityConfidence["tier"];
  confidence_reasons: string[];
};

const W_SOURCES = 0.40;
const W_VERIFIED = 0.50;
const W_CANONICAL_URL = 0.15;
const W_DISPLAY_NAME = 0.10;
const W_CROSS_PLATFORM = 0.05;

export function scoreIdentities(
  identities: DedupedIdentity[],
): ScoredIdentity[] {
  // Pre-compute cross-platform handle clusters (handle string seen on
  // ≥2 distinct platforms across the result set).
  const handleToPlatforms = new Map<string, Set<string>>();
  for (const i of identities) {
    if (!i.handle) continue;
    const set = handleToPlatforms.get(i.handle.toLowerCase()) ?? new Set();
    set.add(i.platform);
    handleToPlatforms.set(i.handle.toLowerCase(), set);
  }

  return identities.map((i) => {
    const reasons: string[] = [];
    let score = 0;

    // Source diversity: 0 sources → 0; 1 → 0.10; 2 → 0.20; 3 → 0.30; 4+ → 0.40
    const srcContribution = Math.min(W_SOURCES, (i.source_count ?? 0) * 0.10);
    score += srcContribution;
    if (srcContribution > 0) {
      reasons.push(
        `+${srcContribution.toFixed(2)} from ${i.source_count} source tool(s): ${i.source_tools.join(", ")}`,
      );
    }

    if (i.verified) {
      score += W_VERIFIED;
      reasons.push(`+${W_VERIFIED.toFixed(2)} cryptographically verified (e.g. Keybase signed proof)`);
    }

    if (i.url) {
      score += W_CANONICAL_URL;
      reasons.push(`+${W_CANONICAL_URL.toFixed(2)} canonical profile URL present`);
    }

    if (i.display_name) {
      score += W_DISPLAY_NAME;
      reasons.push(`+${W_DISPLAY_NAME.toFixed(2)} display name fetched (real profile metadata)`);
    }

    const platforms = handleToPlatforms.get(i.handle?.toLowerCase() ?? "");
    if (platforms && platforms.size >= 2) {
      score += W_CROSS_PLATFORM;
      reasons.push(
        `+${W_CROSS_PLATFORM.toFixed(2)} same handle on ${platforms.size} platforms: ${[...platforms].sort().join(", ")}`,
      );
    }

    if (score > 1.0) score = 1.0;
    score = Math.round(score * 1000) / 1000;

    // Tier semantics: cryptographic verification is the strongest signal
    // regardless of corroboration count, so it promotes the tier
    // categorically. Score remains the natural weighted sum so callers
    // that want the raw signal-strength can still see "1-source crypto"
    // (~0.75) as numerically lower than "4-source non-crypto" (0.70).
    // Tier and score decouple here: the tier expresses the categorical
    // decision; the score expresses the underlying signal weight.
    let tier: IdentityConfidence["tier"];
    if (i.verified) {
      tier = "cryptographic";
    } else if (score >= 0.70) {
      tier = "strongly-verified";
    } else if (score >= 0.40) {
      tier = "corroborated";
    } else {
      tier = "single-source";
    }

    return {
      ...i,
      confidence: score,
      tier,
      confidence_reasons: reasons,
    };
  });
}

/** Returns a histogram of the tier counts. Useful for top-level
 *  evidence-strength reporting in the person_aggregate response. */
export function tierHistogram(scored: ScoredIdentity[]): Record<IdentityConfidence["tier"], number> {
  const h = {
    "single-source": 0,
    "corroborated": 0,
    "strongly-verified": 0,
    "cryptographic": 0,
  };
  for (const s of scored) h[s.tier]++;
  return h;
}
