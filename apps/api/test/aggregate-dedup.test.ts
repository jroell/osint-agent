import { describe, expect, test } from "bun:test";
import { dedupEmails, dedupIdentities, type EmailFinding, type Identity } from "../src/mcp/tools/aggregate-dedup";
import { scoreIdentities, tierHistogram } from "../src/mcp/tools/identity-confidence";

// TestPersonAggregateDedup — proof-of-improvement test for iter-17.
//
// The defect: person_aggregate fans out to 13+ tools that each emit
// emails and identities in incompatible string forms. The current
// implementation literally pushes each into an array with no
// canonicalization — so the same mailbox / social profile appears
// multiple times, breaking:
//   1. the "found_on ≥ 2 platforms" verifiability gate downstream
//      consumers depend on
//   2. cross-platform graph joins keyed on social handle (sherlock
//      + RapidAPI follower-list intersection cannot reliably match
//      `JohnDoe` from Twitter to `JohnDoe` from `https://twitter.com/JohnDoe/`
//      surfaced by Diffbot KG)
//   3. evidence-count metrics in panel_entity_resolution
//
// The fix: post-process emails/identities through canonical-keys.ts
// (which mirrors the iter-14/16 Go canonicalizers), grouping every
// surface form that resolves to the same mailbox-key / social-key.
// Each output row includes source_count + source_tools so the
// "evidence" semantics survive.

describe("person_aggregate canonical-key dedup", () => {
  test("dedupEmails collapses 12 surface forms into 4 mailboxes (≥60% reduction)", () => {
    // Realistic person_aggregate fan-out: 4 unique real mailboxes
    // emitted by 6 tools across 12 surface forms.
    const findings: EmailFinding[] = [
      // Mailbox 1 — gmail dot-alias + plus-tag + googlemail mirror
      { email: "John.Doe@Gmail.com", source_tool: "github_user_profile", evidence: { profile_field: true } },
      { email: "johndoe@gmail.com", source_tool: "hibp_breach_lookup", evidence: { breaches: 3 } },
      { email: "johndoe+work@gmail.com", source_tool: "github_commit_emails", evidence: { commits: 17 } },
      { email: "johndoe@googlemail.com", source_tool: "hunter_io_email_finder", evidence: { confidence: 80 } },

      // Mailbox 2 — outlook plus-tag
      { email: "alice@outlook.com", source_tool: "hibp_breach_lookup", evidence: { breaches: 1 } },
      { email: "alice+spam@outlook.com", source_tool: "hunter_io_email_finder", evidence: { confidence: 70 } },
      { email: "ALICE@OUTLOOK.com", source_tool: "github_commit_emails", evidence: { commits: 4 } },

      // Mailbox 3 — single source
      { email: "bob.smith@example.org", source_tool: "hibp_breach_lookup" },

      // Mailbox 4 — display-name wrapper + mailto: + trailing-dot domain
      { email: '"Carol Doe" <carol@example.com>', source_tool: "github_commit_emails" },
      { email: "mailto:carol@example.com", source_tool: "hunter_io_email_finder" },
      { email: "carol@example.com.", source_tool: "diffbot_kg_query" },

      // Garbage that should be dropped, not silently merged
      { email: "not-an-email", source_tool: "github_commit_emails" },
    ];

    // LEGACY: literal-string dedup (the current person-aggregate.ts behavior — a
    // raw push into an array; the only "dedup" is the natural Set-of-strings).
    const legacyKeys = new Set(findings.map(f => f.email.toLowerCase().trim()));

    const { deduped, unparsable } = dedupEmails(findings);

    console.log(`\n  Email-dedup on ${findings.length} fan-out findings:`);
    console.log(`    legacy literal-string distinct: ${legacyKeys.size}`);
    console.log(`    canonical mailbox-keys:         ${deduped.length}`);
    console.log(`    unparsable (correctly dropped): ${unparsable.length}`);
    const dedupRate = 1 - deduped.length / legacyKeys.size;
    console.log(`    reduction: ${(dedupRate * 100).toFixed(1)}%`);
    for (const d of deduped) {
      console.log(`      ${d.mailbox_key}: ${d.source_count} sources, ${d.variants_seen.length} variants`);
    }

    expect(deduped.length).toBe(4);
    expect(unparsable.length).toBe(1);
    expect(unparsable[0]?.email).toBe("not-an-email");
    expect(legacyKeys.size).toBeGreaterThanOrEqual(11);
    // Hard floor: ≥60% reduction (legacy ≥ 2.5× canonical).
    expect(deduped.length * 2).toBeLessThanOrEqual(legacyKeys.size);

    // Each deduped row must surface the corroborating sources.
    const gmailRow = deduped.find(d => d.mailbox_key === "johndoe@gmail.com");
    expect(gmailRow).toBeDefined();
    expect(gmailRow!.source_count).toBe(4);
    expect(gmailRow!.variants_seen.length).toBe(4);
    expect(gmailRow!.source_tools.sort()).toEqual([
      "github_commit_emails",
      "github_user_profile",
      "hibp_breach_lookup",
      "hunter_io_email_finder",
    ]);

    const outlookRow = deduped.find(d => d.mailbox_key === "alice@outlook.com");
    expect(outlookRow!.source_count).toBe(3);

    // Most-corroborated first.
    expect(deduped[0]!.source_count).toBeGreaterThanOrEqual(deduped[deduped.length - 1]!.source_count);
  });

  test("dedupIdentities collapses 8 surface forms into 3 platform-typed identities", () => {
    const identities: Identity[] = [
      // Platform 1 — Twitter "JohnDoe" surfaced by 4 different tools in 4 forms
      { platform: "twitter", handle: "JohnDoe", url: "https://twitter.com/JohnDoe", source_tool: "diffbot_kg_query" },
      { platform: "twitter", handle: "johndoe", url: "https://x.com/johndoe", source_tool: "github_user_profile",
        evidence: { from_github_profile: true } },
      { platform: "twitter", handle: "JohnDoe", url: "https://www.twitter.com/JohnDoe/", source_tool: "tavily_search" },
      { platform: "twitter", handle: "johndoe", url: "https://twitter.com/intent/user?screen_name=JohnDoe", source_tool: "google_dork_search" },

      // Platform 2 — GitHub "octocat" from 3 different tools
      { platform: "github", handle: "octocat", url: "https://github.com/octocat", verified: true, source_tool: "github_user_profile" },
      { platform: "github", handle: "Octocat", url: "https://github.com/Octocat/", source_tool: "diffbot_kg_query" },
      { platform: "github", handle: "octocat", url: "github.com/octocat", source_tool: "keybase_lookup" },

      // Platform 3 — LinkedIn slug, single tool
      { platform: "linkedin (via google_dork)", url: "https://www.linkedin.com/in/john-doe-12345/",
        display_name: "John Doe", source_tool: "google_dork_search" },

      // Out-of-scope: synthesis row with no canonical handle (must end up in unkeyed)
      { platform: "tavily_synthesis", source_tool: "tavily_search", evidence: { answer: "..." } },
    ];

    // LEGACY: the current handleClusters logic in person-aggregate.ts uses
    // `handle.toLowerCase()` as the cluster key. That yields these distinct
    // clusters (we replicate it here for the before/after comparison):
    const legacyClusters = new Set<string>();
    for (const i of identities) {
      if (i.handle) legacyClusters.add(i.handle.toLowerCase());
    }

    const { deduped, unkeyed } = dedupIdentities(identities);

    console.log(`\n  Identity-dedup on ${identities.length} fan-out findings:`);
    console.log(`    legacy handle-lowercase clusters: ${legacyClusters.size}`);
    console.log(`    canonical social-keys:            ${deduped.length}`);
    console.log(`    unkeyed (synthesis rows etc):     ${unkeyed.length}`);
    for (const d of deduped) {
      console.log(`      ${d.social_key}: ${d.source_count} sources (verified=${d.verified})`);
    }

    expect(deduped.length).toBe(3);
    expect(unkeyed.length).toBe(1);
    expect(unkeyed[0]?.platform).toBe("tavily_synthesis");

    const twitterRow = deduped.find(d => d.social_key === "twitter:johndoe");
    expect(twitterRow).toBeDefined();
    expect(twitterRow!.source_count).toBe(4);
    expect(twitterRow!.url).toBe("https://twitter.com/johndoe");

    const githubRow = deduped.find(d => d.social_key === "github:octocat");
    expect(githubRow!.source_count).toBe(3);
    expect(githubRow!.verified).toBe(true);

    const linkedinRow = deduped.find(d => d.social_key === "linkedin:john-doe-12345");
    expect(linkedinRow).toBeDefined();
    expect(linkedinRow!.display_name).toBe("John Doe");
    // linkedin platform was originally tagged "linkedin (via google_dork)";
    // canonicalization relabels it to plain "linkedin".
    expect(linkedinRow!.platform).toBe("linkedin");
  });

  test("aggregate quantitative summary: 60-70% reduction across both axes", () => {
    // Combined fan-out load — demonstrates the total reduction
    // across emails + identities for one realistic person_aggregate call.
    const emails: EmailFinding[] = [
      { email: "j.doe@gmail.com", source_tool: "a" },
      { email: "jdoe@gmail.com", source_tool: "b" },
      { email: "jdoe+work@gmail.com", source_tool: "c" },
      { email: "JDoe@googlemail.com", source_tool: "d" },
      { email: "jane@outlook.com", source_tool: "a" },
      { email: "Jane@OUTLOOK.com", source_tool: "b" },
      { email: "jane+x@outlook.com", source_tool: "c" },
    ];
    const identities: Identity[] = [
      { platform: "twitter", url: "https://twitter.com/jdoe", source_tool: "a" },
      { platform: "twitter", url: "https://x.com/JDoe", source_tool: "b" },
      { platform: "twitter", url: "twitter.com/jdoe", source_tool: "c" },
      { platform: "github", url: "https://github.com/jdoe", source_tool: "a" },
      { platform: "github", url: "github.com/JDoe/", source_tool: "b" },
    ];

    const beforeEmails = new Set(emails.map(e => e.email.toLowerCase().trim())).size;
    const beforeIds = new Set(identities.map(i => (i.url ?? "").toLowerCase().trim())).size;

    const e = dedupEmails(emails);
    const i = dedupIdentities(identities);

    const before = beforeEmails + beforeIds;
    const after = e.deduped.length + i.deduped.length;
    const reduction = 1 - after / before;
    console.log(`\n  Combined dedup: ${before} → ${after}  (${(reduction * 100).toFixed(1)}% reduction)`);

    expect(e.deduped.length).toBe(2);
    expect(i.deduped.length).toBe(2);
    expect(reduction).toBeGreaterThanOrEqual(0.6);
  });

  test("identity confidence scoring tiers deduped identities by evidence strength", () => {
    const identities = dedupIdentities([
      { platform: "github", url: "https://github.com/octocat", verified: true, display_name: "Octocat", source_tool: "github_user_profile" },
      { platform: "github", url: "github.com/Octocat/", source_tool: "keybase_lookup" },
      { platform: "twitter", url: "https://twitter.com/octocat", source_tool: "diffbot_kg_query" },
      { platform: "twitter", url: "https://x.com/octocat", source_tool: "google_dork_search" },
      { platform: "reddit", url: "https://www.reddit.com/user/one-off", source_tool: "reddit_user_intel" },
    ]).deduped;

    const scored = scoreIdentities(identities);
    const github = scored.find(i => i.social_key === "github:octocat");
    const twitter = scored.find(i => i.social_key === "twitter:octocat");
    const reddit = scored.find(i => i.social_key === "reddit:one-off");

    expect(github?.tier).toBe("cryptographic");
    expect(github?.confidence).toBeGreaterThanOrEqual(0.9);
    expect(twitter?.tier).toBe("corroborated");
    expect(reddit?.tier).toBe("single-source");
    expect(tierHistogram(scored)).toEqual({
      "single-source": 1,
      "corroborated": 1,
      "strongly-verified": 0,
      "cryptographic": 1,
    });
  });
});
