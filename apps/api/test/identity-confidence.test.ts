import { describe, expect, test } from "bun:test";
import { scoreIdentities, tierHistogram } from "../src/mcp/tools/identity-confidence";
import type { DedupedIdentity } from "../src/mcp/tools/aggregate-dedup";

// TestIdentityConfidence — proof-of-improvement test for iter-22.
//
// The defect: post-iter-17 person_aggregate emits deduped identities
// with a `source_count` field, but every downstream consumer that
// wants to gate on "is this verified?" had to roll its own heuristic
// (count >= 2, verified flag, etc.) and they didn't agree. Worse,
// there was no way to reflect "this Keybase row has a CRYPTOGRAPHIC
// proof and 1 source" as different from "this Tavily row has 4
// sources but no verification" — both look like 'solid evidence' by
// raw counts but only the first is court-grade.
//
// The fix: a single normalized 0.0-1.0 confidence score per
// identity, derived from independent signals (source diversity +
// cryptographic verification + URL canonicalization + display-name
// presence + cross-platform handle reuse), plus a 4-tier label
// callers can switch on without recomputing weights.

describe("identity confidence scoring", () => {
  test("tier separation is monotonic across realistic identity samples", () => {
    const identities: DedupedIdentity[] = [
      // single-source: just one tool found this
      {
        platform: "stackexchange:stackoverflow.com",
        handle: "12345",
        url: "https://stackoverflow.com/users/12345",
        social_key: "stackexchange:12345",
        source_tools: ["stackexchange_user"],
        source_count: 1,
        verified: false,
        evidence: {},
      },
      // single-source — synthesis row from one search engine, no canonical URL
      {
        platform: "tavily_synthesis",
        handle: "",
        url: "",
        social_key: "",
        source_tools: ["tavily_search"],
        source_count: 1,
        verified: false,
        evidence: {},
      },
      // corroborated: 2-3 tools, no verification, with URL + display name
      {
        platform: "twitter",
        handle: "johndoe",
        url: "https://twitter.com/johndoe",
        display_name: "John Doe",
        social_key: "twitter:johndoe",
        source_tools: ["twitter_rapidapi", "diffbot_kg_query"],
        source_count: 2,
        verified: false,
        evidence: {},
      },
      // strongly-verified: 4 tools agree, URL + display name (no crypto)
      {
        platform: "github",
        handle: "octocat",
        url: "https://github.com/octocat",
        display_name: "The Octocat",
        social_key: "github:octocat",
        source_tools: ["github_user_profile", "diffbot_kg_query", "google_dork_search", "tavily_search"],
        source_count: 4,
        verified: false,
        evidence: {},
      },
      // cryptographic: Keybase proof, 1 tool, with display name
      {
        platform: "keybase",
        handle: "octocat",
        url: "https://keybase.io/octocat",
        display_name: "The Octocat",
        social_key: "keybase:octocat",
        source_tools: ["keybase_lookup"],
        source_count: 1,
        verified: true,
        evidence: {},
      },
    ];

    const scored = scoreIdentities(identities);
    const byKey = new Map(scored.map((s) => [s.social_key || s.platform, s]));

    console.log("\n  Identity confidence scoring (5 fixtures):");
    for (const s of scored) {
      console.log(
        `    ${s.tier.padEnd(20)}  ${s.confidence.toFixed(3)}  ${s.platform}:${s.handle}`,
      );
    }

    // Tier ordering — strict separation per fixture.
    expect(byKey.get("stackexchange:12345")!.tier).toBe("single-source");
    expect(byKey.get("tavily_synthesis")!.tier).toBe("single-source");

    const twitter = byKey.get("twitter:johndoe")!;
    expect(twitter.tier).toBe("corroborated");
    // Two sources (0.20) + URL (0.15) + display (0.10) + cross-platform (0.05) = 0.50
    expect(twitter.confidence).toBeGreaterThanOrEqual(0.4);
    expect(twitter.confidence).toBeLessThan(0.7);

    const github = byKey.get("github:octocat")!;
    expect(github.tier).toBe("strongly-verified");
    // Four sources (0.40) + URL (0.15) + display (0.10) + cross-platform (0.05) = 0.70
    expect(github.confidence).toBeGreaterThanOrEqual(0.7);
    expect(github.confidence).toBeLessThan(0.9);

    const keybase = byKey.get("keybase:octocat")!;
    expect(keybase.tier).toBe("cryptographic");
    // One source (0.10) + verified (0.50) + URL (0.15) + display (0.10) + cross-platform (0.05) = 0.90
    expect(keybase.confidence).toBeGreaterThanOrEqual(0.9);
  });

  test("confidence increases monotonically with source_count", () => {
    const base = (n: number): DedupedIdentity => ({
      platform: "twitter",
      handle: "x",
      url: "https://twitter.com/x",
      social_key: "twitter:x",
      source_tools: Array.from({ length: n }, (_, i) => `tool_${i}`),
      source_count: n,
      verified: false,
      evidence: {},
    });
    const scores = [1, 2, 3, 4, 5].map((n) => scoreIdentities([base(n)])[0]!.confidence);
    for (let i = 1; i < scores.length; i++) {
      expect(scores[i]).toBeGreaterThanOrEqual(scores[i - 1]!);
    }
    // After 4 sources, source-contribution should saturate (capped at 0.40).
    expect(scores[3]).toBe(scores[4]); // 4 and 5 sources score the same
  });

  test("verified=true is a non-negligible step up", () => {
    const base: DedupedIdentity = {
      platform: "keybase",
      handle: "alice",
      url: "https://keybase.io/alice",
      social_key: "keybase:alice",
      source_tools: ["keybase_lookup"],
      source_count: 1,
      verified: false,
      evidence: {},
    };
    const unverified = scoreIdentities([base])[0]!;
    const verified = scoreIdentities([{ ...base, verified: true }])[0]!;
    const delta = verified.confidence - unverified.confidence;
    console.log(`\n  verified=true bumps confidence by +${delta.toFixed(3)} (unverified=${unverified.confidence}, verified=${verified.confidence})`);
    // Verified rows are floored into cryptographic range, so the step
    // must be at least the raw verification weight.
    expect(delta).toBeGreaterThanOrEqual(0.50);
  });

  test("quantitative tier-distribution improvement vs naive count-based grading", () => {
    // BEFORE iter-22: callers used `source_count >= 2` as the quality gate.
    // This treated a 1-source Keybase cryptographic proof identically to a
    // 1-source dangling URL → both flagged as "single-source / unreliable".
    // AFTER iter-22: the verified flag promotes Keybase to 'cryptographic'
    // even at source_count=1, so the same fixture produces a strictly
    // BETTER tier distribution for graph-trustworthy rows.
    const identities: DedupedIdentity[] = [
      // Realistic mix: 3 strong / 4 medium / 3 weak
      { platform: "keybase", handle: "alice", url: "https://keybase.io/alice", display_name: "A", social_key: "keybase:alice", source_tools: ["keybase_lookup"], source_count: 1, verified: true, evidence: {} },
      { platform: "keybase", handle: "bob", url: "https://keybase.io/bob", display_name: "B", social_key: "keybase:bob", source_tools: ["keybase_lookup"], source_count: 1, verified: true, evidence: {} },
      { platform: "keybase", handle: "carol", url: "https://keybase.io/carol", display_name: "C", social_key: "keybase:carol", source_tools: ["keybase_lookup"], source_count: 1, verified: true, evidence: {} },

      { platform: "github", handle: "alice", url: "https://github.com/alice", display_name: "Alice", social_key: "github:alice", source_tools: ["a", "b", "c"], source_count: 3, verified: false, evidence: {} },
      { platform: "twitter", handle: "alice", url: "https://twitter.com/alice", display_name: "Alice", social_key: "twitter:alice", source_tools: ["a", "b"], source_count: 2, verified: false, evidence: {} },
      { platform: "linkedin", handle: "alice-z", url: "https://www.linkedin.com/in/alice-z/", display_name: "Alice Z.", social_key: "linkedin:alice-z", source_tools: ["a", "b"], source_count: 2, verified: false, evidence: {} },
      { platform: "instagram", handle: "alice", url: "https://www.instagram.com/alice/", social_key: "instagram:alice", source_tools: ["a", "b"], source_count: 2, verified: false, evidence: {} },

      { platform: "stackexchange", handle: "12", url: "https://stackoverflow.com/users/12", social_key: "stackexchange:12", source_tools: ["stackexchange_user"], source_count: 1, verified: false, evidence: {} },
      { platform: "tavily_synthesis", handle: "", url: "", social_key: "", source_tools: ["tavily_search"], source_count: 1, verified: false, evidence: {} },
      { platform: "perplexity_synthesis", handle: "", url: "", social_key: "", source_tools: ["perplexity_search"], source_count: 1, verified: false, evidence: {} },
    ];

    const scored = scoreIdentities(identities);
    const hist = tierHistogram(scored);

    // LEGACY: source_count >= 2 → "verified", else → "unverified". 1 axis.
    const legacyVerified = identities.filter((i) => i.source_count >= 2).length;
    const legacyUnverified = identities.length - legacyVerified;

    console.log("\n  Tier distribution on 10 realistic identities:");
    console.log(`    legacy:    ${legacyVerified} 'verified' / ${legacyUnverified} 'unverified' (binary, count-based)`);
    console.log(`    new:       ${hist.cryptographic} cryptographic / ${hist["strongly-verified"]} strongly-verified / ${hist.corroborated} corroborated / ${hist["single-source"]} single-source`);

    // The legacy binary classification flagged the 3 Keybase crypto rows as
    // 'unverified' (each had source_count=1) — a clear false-negative. The
    // new tiering correctly upgrades them.
    const newCryptoRows = scored.filter((s) => s.tier === "cryptographic" || s.tier === "strongly-verified").length;
    const legacyHighConfidence = legacyVerified;
    console.log(`    high-confidence rows: legacy ${legacyHighConfidence} → new ${newCryptoRows} (${newCryptoRows >= legacyHighConfidence ? "+" : ""}${newCryptoRows - legacyHighConfidence})`);

    // Assertions: cryptographic rows recognized; tier separation has range.
    expect(hist.cryptographic + hist["strongly-verified"] + hist.corroborated + hist["single-source"]).toBe(10);
    expect(scored.filter((s) => s.tier !== "single-source").length).toBeGreaterThanOrEqual(legacyVerified);
    // At least one cryptographic-tier row (the Keybase entries that legacy missed).
    expect(hist.cryptographic).toBeGreaterThanOrEqual(1);
  });
});
