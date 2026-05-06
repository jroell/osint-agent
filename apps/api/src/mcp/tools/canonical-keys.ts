// Canonical-key helpers used by person_aggregate and panel_entity_resolution
// to dedup emails and social-handle identities found across multiple OSINT
// tools. These are TS ports of the well-tested Go canonicalizers in
// apps/go-worker/internal/tools/entity_match.go (mode email_canonicalize,
// mode social_canonicalize). Done in-process to avoid 10-40 worker RPC
// round-trips per person_aggregate call.
//
// If you change rules here, update the Go side too. Both have unit tests
// pinning the canonical forms.

const EMAIL_PROVIDER_BY_DOMAIN: Record<string, string> = {
  "gmail.com": "gmail",
  "googlemail.com": "gmail",
  "outlook.com": "outlook",
  "hotmail.com": "outlook",
  "live.com": "outlook",
  "msn.com": "outlook",
  "yahoo.com": "yahoo",
  "yahoo.co.uk": "yahoo",
  "ymail.com": "yahoo",
  "icloud.com": "icloud",
  "me.com": "icloud",
  "mac.com": "icloud",
  "proton.me": "proton",
  "protonmail.com": "proton",
  "pm.me": "proton",
  "fastmail.com": "fastmail",
  "fastmail.fm": "fastmail",
};

/** Returns the strongest dedup key for an email address — same mailbox-key
 *  for every textual form that resolves to the same real mailbox.
 *  Returns null if the input isn't a parsable email. */
export function emailMailboxKey(raw: string): string | null {
  if (!raw) return null;
  let s = raw.trim();
  // Display-name wrapper: "Name <addr>" → "addr"
  const lt = s.lastIndexOf("<");
  if (lt >= 0) {
    const gt = s.indexOf(">", lt);
    if (gt > lt) s = s.slice(lt + 1, gt);
  }
  s = s.trim();
  if (s.toLowerCase().startsWith("mailto:")) s = s.slice(7);
  if (s.includes("%")) {
    try {
      s = decodeURIComponent(s);
    } catch {
      // best-effort
    }
  }
  s = s.trim().toLowerCase();
  if (s.split("@").length !== 2) return null;
  const [local, domainRaw] = s.split("@");
  if (!local || !domainRaw) return null;
  if (/[\s\t]/.test(local)) return null;
  const domain = domainRaw.replace(/\.$/, "");
  if (!domain) return null;

  const provider = EMAIL_PROVIDER_BY_DOMAIN[domain] ?? "other";

  let mailboxLocal = local;
  // Plus-tag stripping for providers that document subaddressing.
  if (["gmail", "outlook", "yahoo", "icloud", "proton", "fastmail"].includes(provider)) {
    const plus = mailboxLocal.indexOf("+");
    if (plus > 0) mailboxLocal = mailboxLocal.slice(0, plus);
  }
  // Gmail dot-aliasing.
  let mailboxDomain = domain;
  if (provider === "gmail") {
    mailboxLocal = mailboxLocal.replace(/\./g, "");
    if (domain === "googlemail.com") mailboxDomain = "gmail.com";
  }
  if (!mailboxLocal) return null;
  return `${mailboxLocal}@${mailboxDomain}`;
}

const SOCIAL_HOSTS: Record<string, string> = {
  "twitter.com": "twitter",
  "x.com": "twitter",
  "mobile.twitter.com": "twitter",
  "m.twitter.com": "twitter",
  "nitter.net": "twitter",
  "instagram.com": "instagram",
  "instagr.am": "instagram",
  "tiktok.com": "tiktok",
  "vm.tiktok.com": "tiktok",
  "linkedin.com": "linkedin",
  "github.com": "github",
  "reddit.com": "reddit",
  "old.reddit.com": "reddit",
  "new.reddit.com": "reddit",
  "np.reddit.com": "reddit",
  "youtube.com": "youtube",
  "m.youtube.com": "youtube",
  "youtu.be": "youtube",
  "facebook.com": "facebook",
  "m.facebook.com": "facebook",
  "fb.com": "facebook",
  "fb.me": "facebook",
  "threads.net": "threads",
  "threads.com": "threads",
  "bsky.app": "bluesky",
  "medium.com": "medium",
  "keybase.io": "keybase",
  "news.ycombinator.com": "hackernews",
};

const TWITTER_RESERVED = new Set([
  "home", "explore", "notifications", "messages", "i", "settings",
  "compose", "search", "hashtag", "intent", "share", "login",
  "signup", "tos", "privacy", "about",
]);
const GITHUB_RESERVED = new Set([
  "settings", "marketplace", "orgs", "trending", "issues", "pulls",
  "explore", "topics", "collections", "about", "login", "join",
  "search", "notifications",
]);

const HANDLE_RE = /^[a-z0-9._@-]+$/;

export type SocialKey = {
  platform: string;
  handle: string;
  /** "<platform>:<handle>" — the dedup primary key. */
  key: string;
  canonicalUrl: string;
};

/** Returns the strongest dedup key for a social handle / profile URL.
 *  Returns null if the input can't be resolved to a known platform.
 *  Recognized: twitter/x, instagram, tiktok, linkedin (in + company),
 *  github, reddit, youtube (@/channel/c/user), facebook, threads,
 *  bluesky, mastodon (@user@instance), hackernews, medium, keybase. */
export function socialKey(raw: string, platformHint?: string): SocialKey | null {
  if (!raw) return null;
  let s = raw.trim().replace(/^["'<]+|["'>]+$/g, "");

  // Mastodon "@user@instance.tld" — must run before URL parse, otherwise
  // the scheme-less fallback would treat instance.tld as the host.
  if (!s.includes("://") && (s.match(/@/g)?.length ?? 0) >= 1) {
    const stripped = s.startsWith("@") ? s.slice(1) : s;
    const at = stripped.indexOf("@");
    if (at > 0) {
      const user = stripped.slice(0, at).toLowerCase().trim();
      const instance = stripped.slice(at + 1).toLowerCase().trim();
      if (instance.includes(".") && HANDLE_RE.test(user) && HANDLE_RE.test(instance)) {
        const handle = `${user}@${instance}`;
        return {
          platform: "mastodon",
          handle,
          key: `mastodon:${handle}`,
          canonicalUrl: `https://${instance}/@${user}`,
        };
      }
    }
  }

  // Try URL parse (scheme-less hosts allowed if first segment has a dot).
  let url: URL | null = null;
  try {
    if (s.includes("://")) {
      url = new URL(s);
    } else {
      const head = s.split(/[/?]/)[0] ?? "";
      if (head.includes(".")) url = new URL(`https://${s}`);
    }
  } catch {
    url = null;
  }

  if (url) {
    const host = url.host.toLowerCase().replace(/^www\./, "");
    const platform = SOCIAL_HOSTS[host];
    const path = url.pathname.replace(/^\/+|\/+$/g, "");
    const first = path.split(/[/?#]/)[0] ?? "";

    if (!platform) {
      // Possible Mastodon instance: /@user
      if (path.startsWith("@")) {
        const user = first.slice(1).toLowerCase();
        if (user && HANDLE_RE.test(user)) {
          const handle = `${user}@${host}`;
          return {
            platform: "mastodon",
            handle,
            key: `mastodon:${handle}`,
            canonicalUrl: `https://${host}/@${user}`,
          };
        }
      }
      return null;
    }

    switch (platform) {
      case "twitter": {
        if (path === "intent/user") {
          const sn = url.searchParams.get("screen_name");
          return sn ? makeKey("twitter", sn.toLowerCase()) : null;
        }
        if (!first || TWITTER_RESERVED.has(first)) return null;
        return makeKey("twitter", first.replace(/^@/, "").toLowerCase());
      }
      case "instagram": {
        if (!first || ["p", "reel", "tv", "explore", "accounts"].includes(first)) return null;
        return makeKey("instagram", first.replace(/^@/, "").toLowerCase());
      }
      case "tiktok": {
        if (!first.startsWith("@")) return null;
        return makeKey("tiktok", first.slice(1).toLowerCase());
      }
      case "linkedin": {
        if (first === "in") {
          const slug = path.slice(3).split(/[/?#]/)[0]?.toLowerCase() ?? "";
          if (!HANDLE_RE.test(slug) || !slug) return null;
          return {
            platform: "linkedin",
            handle: slug,
            key: `linkedin:${slug}`,
            canonicalUrl: `https://www.linkedin.com/in/${slug}/`,
          };
        }
        if (first === "company") {
          const slug = path.slice("company/".length).split(/[/?#]/)[0]?.toLowerCase() ?? "";
          if (!HANDLE_RE.test(slug) || !slug) return null;
          return {
            platform: "linkedin-company",
            handle: slug,
            key: `linkedin-company:${slug}`,
            canonicalUrl: `https://www.linkedin.com/company/${slug}/`,
          };
        }
        return null;
      }
      case "github": {
        if (!first || GITHUB_RESERVED.has(first)) return null;
        return makeKey("github", first.toLowerCase());
      }
      case "reddit": {
        if (first === "user" || first === "u") {
          const handle = path.slice(first.length + 1).split(/[/?#]/)[0]?.toLowerCase() ?? "";
          if (!HANDLE_RE.test(handle) || !handle) return null;
          return makeKey("reddit", handle);
        }
        return null;
      }
      case "youtube": {
        if (path.startsWith("@")) {
          const handle = first.slice(1).toLowerCase();
          if (!HANDLE_RE.test(handle) || !handle) return null;
          return makeKey("youtube", handle);
        }
        if (first === "c" || first === "user") {
          const handle = path.slice(first.length + 1).split(/[/?#]/)[0]?.toLowerCase() ?? "";
          if (!HANDLE_RE.test(handle) || !handle) return null;
          return makeKey("youtube", handle);
        }
        if (first === "channel") {
          const id = path.slice("channel/".length).split(/[/?#]/)[0] ?? "";
          if (!HANDLE_RE.test(id.toLowerCase()) || !id) return null;
          return {
            platform: "youtube-channel",
            handle: id,
            key: `youtube-channel:${id}`,
            canonicalUrl: `https://www.youtube.com/channel/${id}`,
          };
        }
        return null;
      }
      case "facebook": {
        if (first === "profile.php") {
          const id = url.searchParams.get("id");
          if (id) {
            return {
              platform: "facebook",
              handle: `id:${id}`,
              key: `facebook:id:${id}`,
              canonicalUrl: `https://www.facebook.com/profile.php?id=${id}`,
            };
          }
          return null;
        }
        if (!first || ["groups", "pages"].includes(first)) return null;
        return makeKey("facebook", first.toLowerCase());
      }
      case "threads": {
        if (!first.startsWith("@")) return null;
        return makeKey("threads", first.slice(1).toLowerCase());
      }
      case "bluesky": {
        if (path.startsWith("profile/")) {
          const handle = path.slice("profile/".length).split(/[/?#]/)[0]?.toLowerCase() ?? "";
          if (!handle) return null;
          return {
            platform: "bluesky",
            handle,
            key: `bluesky:${handle}`,
            canonicalUrl: `https://bsky.app/profile/${handle}`,
          };
        }
        return null;
      }
      case "medium": {
        if (!first) return null;
        return makeKey("medium", first.replace(/^@/, "").toLowerCase());
      }
      case "keybase": {
        if (!first) return null;
        return makeKey("keybase", first.toLowerCase());
      }
      case "hackernews": {
        if (path.startsWith("user")) {
          const id = url.searchParams.get("id");
          if (id) {
            return {
              platform: "hackernews",
              handle: id,
              key: `hackernews:${id.toLowerCase()}`,
              canonicalUrl: `https://news.ycombinator.com/user?id=${id}`,
            };
          }
        }
        return null;
      }
    }
  }

  // Bare handle path: optional Reddit shorthand, otherwise needs a hint.
  const lower = s.toLowerCase();
  if (lower.startsWith("/u/") || lower.startsWith("u/")) {
    const handle = lower.replace(/^\/?u\//, "");
    if (!HANDLE_RE.test(handle) || !handle) return null;
    return makeKey("reddit", handle);
  }
  const bare = s.replace(/^@/, "").toLowerCase();
  if (!HANDLE_RE.test(bare) || !bare) return null;
  if (platformHint) {
    return makeKey(platformHint.toLowerCase(), bare);
  }
  return {
    platform: "unknown",
    handle: bare,
    key: `unknown:${bare}`,
    canonicalUrl: "",
  };
}

function makeKey(platform: string, handle: string): SocialKey | null {
  if (!HANDLE_RE.test(handle) || !handle) return null;
  return {
    platform,
    handle,
    key: `${platform}:${handle}`,
    canonicalUrl: canonicalProfileUrl(platform, handle),
  };
}

function canonicalProfileUrl(platform: string, handle: string): string {
  switch (platform) {
    case "twitter": return `https://twitter.com/${handle}`;
    case "instagram": return `https://www.instagram.com/${handle}/`;
    case "tiktok": return `https://www.tiktok.com/@${handle}`;
    case "linkedin": return `https://www.linkedin.com/in/${handle}/`;
    case "linkedin-company": return `https://www.linkedin.com/company/${handle}/`;
    case "github": return `https://github.com/${handle}`;
    case "reddit": return `https://www.reddit.com/user/${handle}`;
    case "youtube": return `https://www.youtube.com/@${handle}`;
    case "youtube-channel": return `https://www.youtube.com/channel/${handle}`;
    case "facebook": return `https://www.facebook.com/${handle}`;
    case "threads": return `https://www.threads.net/@${handle}`;
    case "bluesky": return `https://bsky.app/profile/${handle}`;
    case "medium": return `https://medium.com/@${handle}`;
    case "keybase": return `https://keybase.io/${handle}`;
    case "hackernews": return `https://news.ycombinator.com/user?id=${handle}`;
    default: return "";
  }
}
