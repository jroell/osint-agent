import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum([
      "name_match",
      "name_variations",
      "username_variations",
      "email_canonicalize",
      "phone_canonicalize",
      "social_canonicalize",
      "url_canonicalize",
      "domain_canonicalize",
      "ip_canonicalize",
    ])
    .optional()
    .describe(
      "name_match: compare two names → similarity scores + verdict. name_variations: name → nickname/phonetic/initial variations. username_variations: full name → cross-platform username candidates. email_canonicalize: a single email → provider-aware canonical form + strongest dedup mailbox-key. phone_canonicalize: a single phone → E.164 canonical form + region + captured extension. social_canonicalize: a profile URL or platform-tagged handle → \"<platform>:<handle>\" dedup key + canonical profile URL. url_canonicalize/domain_canonicalize/ip_canonicalize: canonical dedup keys for web URLs, registered domains, and IP addresses. Auto-detects: name_a → name_match, full_name → username_variations, email → email_canonicalize, phone → phone_canonicalize, social → social_canonicalize, url → url_canonicalize, domain → domain_canonicalize, ip → ip_canonicalize, else → name_variations.",
    ),
  name_a: z.string().optional().describe("First name to compare (name_match mode)."),
  name_b: z.string().optional().describe("Second name to compare (name_match mode)."),
  name: z.string().optional().describe("Name to expand (name_variations mode). Can include surname; only the given name is expanded."),
  full_name: z
    .string()
    .optional()
    .describe("Full name (username_variations mode), e.g. 'John Q. Doe Jr.'. Honorifics + suffixes auto-stripped."),
  email: z
    .string()
    .optional()
    .describe(
      "Email address (email_canonicalize mode). Tolerant of mailto: prefix, percent-encoding, display-name wrappers, surrounding whitespace, and IDN punycode/Unicode forms. Returns email_canonical (deliverable) and email_mailbox_key (strongest dedup key, includes gmail dot-aliasing). Use email_mailbox_key as the primary key when merging email-emitting tool outputs (github_emails, hibp, holehe, hunter_io, dehashed, mail_correlate, ghunt, gravatar, keybase, etc.) into person_aggregate / panel_entity_resolution.",
    ),
  phone: z
    .string()
    .optional()
    .describe(
      "Phone number (phone_canonicalize mode). Tolerant of every common surface form: parens / dashes / dots / spaces, tel:+ scheme, percent-encoding, 00-international prefix, NANP shorthand without country code (10-digit), extensions ('ext 123', 'x123', ';ext=123', '#123', 'p123'). Returns phone_e164 (strongest dedup key, e.g. '+14155552671'), phone_region (ISO-3166), phone_extension (captured separately so the dedup key stays clean), phone_toll_free (NANP 8XX flag). Use phone_e164 as primary key when merging phone-emitting tool outputs (people_data_labs, truepeoplesearch_lookup, hunter_io, holehe, hudsonrock_cavalier, phone_numverify, panel_entity_resolution).",
    ),
  social: z
    .string()
    .optional()
    .describe(
      "Social profile URL or handle (social_canonicalize mode). Resolves any of: full profile URLs (twitter.com/x.com mirror, instagram, tiktok @, linkedin /in/ + /company/, github, reddit /user/ + /u/, youtube /@ + /channel/ + /c/ + /user/, facebook, threads, bluesky, mastodon /@, hackernews, medium, substack), scheme-less hosts (e.g. 'github.com/octocat'), Twitter intent URLs (?screen_name=), Mastodon-style fediverse handles ('@user@instance.tld'), Reddit shorthand ('u/spez', '/u/spez'). Returns social_key '<platform>:<handle>' as the strongest dedup primary key — use it as the cross-platform graph join key when merging outputs from sherlock, maigret, holehe, ghunt, twitter_user, instagram_user, tiktok_lookup, github_user, linkedin_proxycurl, reddit_user_intel, etc.",
    ),
  platform: z
    .string()
    .optional()
    .describe(
      "Optional platform hint for social_canonicalize when the input is a bare handle ('@johndoe' with no URL to disambiguate). Accepted values: twitter, instagram, tiktok, linkedin, github, reddit, youtube, facebook, mastodon, bluesky, threads, medium, substack, keybase. Without a hint, bare handles resolve to platform='unknown'.",
    ),
  url: z
    .string()
    .optional()
    .describe(
      "Web URL (url_canonicalize mode). Returns url_canonical as the strongest dedup key — same string for every textual form of the same logical resource. Rules: http→https, lowercase scheme/host, strip 'www.', strip default ports, IDN→punycode, drop fragment, drop tracking params (utm_*/mc_*/_hs*/fbclid/gclid/yclid/msclkid/igshid/dclid/ref/source/share/etc.), sort remaining params, strip trailing slash on non-root paths. Use url_canonical as primary key when merging URL-emitting tool outputs (firecrawl, google_dork, hackertarget, wayback, common_crawl, github_advanced_search, citation lists from tavily/perplexity).",
    ),
  domain: z
    .string()
    .optional()
    .describe(
      "Hostname or domain (domain_canonicalize mode). Returns domain_apex (the eTLD+1 / registered domain via the IANA Public Suffix List) as the 'is this the same organization?' dedup key. Strips schemes/paths/ports/wildcards/leading-@/trailing-dot, lowercases, IDN→punycode. Distinguishes ICANN suffixes (.com, .co.uk) from PRIVATE suffixes (blogspot.com, github.io, herokuapp.com) via domain_icann — for private suffixes, two subdomains of the same private suffix represent INDEPENDENT users, not the same org. Use domain_apex as the primary key when merging hostname-emitting tool outputs (whois, dns_lookup, asn, http_probe, ssl_cert_chain_inspect, port_scan, censys, shodan, subfinder, ct_brand_watch, takeover, securitytrails, urlscan, reverse_dns).",
    ),
  ip: z
    .string()
    .optional()
    .describe(
      "IP address (ip_canonicalize mode). Returns ip_canonical as the strongest dedup key. Handles IPv4 with leading zeros, ports, CIDR suffixes, bracketed IPv6, IPv4-in-IPv6 wrappers, and RFC 5952 IPv6 compression. Also returns ip_version, ip_class, and flags for IPv4-in-IPv6 / leading-zero / bracketed inputs. Use ip_canonical when merging IP-emitting outputs from shodan, censys, urlscan, dns_lookup, reverse_dns, http_probe, port_scan, ssl_cert_chain_inspect, and exposed_assets.",
    ),
});

toolRegistry.register({
  name: "entity_match",
  description:
    "**Pure-compute ER helper — no external APIs, instant, no rate limits, multiplies recall on every name-based search across the catalog.** Nine modes: (1) **name_match** — compare two names, returns similarity scores + verdict. (2) **name_variations** — nickname/formal/phonetic/initial variants. (3) **username_variations** — cross-platform username candidates. (4) **email_canonicalize** — provider-aware mailbox dedup. (5) **phone_canonicalize** — E.164 phone dedup. (6) **social_canonicalize** — profile URL / handle dedup. (7) **url_canonicalize** — web URL dedup with tracking-param stripping. (8) **domain_canonicalize** — registered-domain eTLD+1 extraction via the IANA Public Suffix List. (9) **ip_canonicalize** — IPv4/IPv6 canonical address dedup, including IPv4-in-IPv6 and RFC 5952 normalization. Pure Go, ~5ms latency.",
  inputSchema: input,
  costMillicredits: 0,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "entity_match",
      input: i,
      timeoutMs: 5_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "entity_match failed");
    return res.output;
  },
});
