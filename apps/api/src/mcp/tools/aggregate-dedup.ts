// person_aggregate post-processing dedup. Uses the canonical-key
// helpers (canonical-keys.ts, mirroring the Go entity_match
// canonicalizers) to collapse multi-tool overlaps so each real-world
// mailbox / social identity appears exactly once with merged evidence.
//
// Without this pass, person_aggregate emits the same identity 3-6 times
// — once per tool that found it — which inflates the dossier, breaks
// "≥2 sources" verifiability gates, and pollutes downstream graph
// joins. See test/aggregate-dedup.test.ts.

import { emailMailboxKey, socialKey } from "./canonical-keys";

export type EmailFinding = {
  email: string;
  source_tool: string;
  evidence?: Record<string, unknown>;
};

export type Identity = {
  platform: string;
  handle?: string;
  url?: string;
  display_name?: string;
  verified?: boolean;
  source_tool: string;
  evidence?: Record<string, unknown>;
};

export type DedupedEmail = {
  email: string;          // canonical/deliverable form (lowercased + +tag-stripped)
  mailbox_key: string;    // strongest dedup key
  source_tools: string[]; // every tool that surfaced this mailbox
  variants_seen: string[]; // unique input strings that resolved here
  evidence: Record<string, unknown>; // shallow-merged evidence across sources
  source_count: number;
};

export type DedupedIdentity = {
  platform: string;
  handle: string;
  url: string;
  display_name?: string;
  verified: boolean;
  social_key: string;
  source_tools: string[];
  evidence: Record<string, unknown>;
  source_count: number;
};

export function dedupEmails(findings: EmailFinding[]): {
  deduped: DedupedEmail[];
  unparsable: EmailFinding[];
} {
  const groups = new Map<string, DedupedEmail>();
  const unparsable: EmailFinding[] = [];
  for (const f of findings) {
    const key = emailMailboxKey(f.email);
    if (!key) {
      unparsable.push(f);
      continue;
    }
    const existing = groups.get(key);
    if (existing) {
      if (!existing.source_tools.includes(f.source_tool)) {
        existing.source_tools.push(f.source_tool);
      }
      if (!existing.variants_seen.includes(f.email)) {
        existing.variants_seen.push(f.email);
      }
      Object.assign(existing.evidence, f.evidence ?? {});
      existing.source_count = existing.source_tools.length;
    } else {
      groups.set(key, {
        email: key, // mailbox_key doubles as the canonical deliverable form for dedup display
        mailbox_key: key,
        source_tools: [f.source_tool],
        variants_seen: [f.email],
        evidence: { ...(f.evidence ?? {}) },
        source_count: 1,
      });
    }
  }
  // Stable order: most-corroborated first, then alphabetical.
  const deduped = [...groups.values()].sort((a, b) => {
    if (b.source_count !== a.source_count) return b.source_count - a.source_count;
    return a.mailbox_key.localeCompare(b.mailbox_key);
  });
  return { deduped, unparsable };
}

export function dedupIdentities(identities: Identity[]): {
  deduped: DedupedIdentity[];
  unkeyed: Identity[];
} {
  const groups = new Map<string, DedupedIdentity>();
  const unkeyed: Identity[] = [];
  for (const i of identities) {
    // Try the URL first (highest-info), then the handle (with platform hint), then fall back.
    const sk =
      (i.url ? socialKey(i.url) : null) ??
      (i.handle ? socialKey(i.handle, mapPlatformToHint(i.platform)) : null);
    if (!sk || sk.platform === "unknown") {
      unkeyed.push(i);
      continue;
    }
    const existing = groups.get(sk.key);
    if (existing) {
      if (!existing.source_tools.includes(i.source_tool)) {
        existing.source_tools.push(i.source_tool);
      }
      // Promote display_name / verified if a later tool provides better info.
      if (!existing.display_name && i.display_name) existing.display_name = i.display_name;
      if (i.verified) existing.verified = true;
      Object.assign(existing.evidence, i.evidence ?? {});
      existing.source_count = existing.source_tools.length;
    } else {
      groups.set(sk.key, {
        platform: sk.platform,
        handle: sk.handle,
        url: sk.canonicalUrl || i.url || "",
        display_name: i.display_name,
        verified: !!i.verified,
        social_key: sk.key,
        source_tools: [i.source_tool],
        evidence: { ...(i.evidence ?? {}) },
        source_count: 1,
      });
    }
  }
  const deduped = [...groups.values()].sort((a, b) => {
    if (b.source_count !== a.source_count) return b.source_count - a.source_count;
    return a.social_key.localeCompare(b.social_key);
  });
  return { deduped, unkeyed };
}

// person_aggregate uses platform tags like "stackexchange:stackoverflow.com",
// "mastodon:mastodon.social", "x_via_grok", "tavily_synthesis" that aren't
// social_canonicalize platforms. Map the few that DO line up; everything
// else becomes undefined (no hint, falls through to "unknown").
function mapPlatformToHint(platform: string): string | undefined {
  const lower = platform.toLowerCase();
  if (
    lower === "twitter" || lower === "instagram" || lower === "tiktok" ||
    lower === "linkedin" || lower === "github" || lower === "reddit" ||
    lower === "youtube" || lower === "facebook" || lower === "threads" ||
    lower === "bluesky" || lower === "mastodon" || lower === "hackernews" ||
    lower === "medium" || lower === "keybase"
  ) return lower;
  return undefined;
}
