import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";

/**
 * MCP prompts: user-invocable investigation playbooks.
 *
 * These are NOT loaded into the default LLM context — clients fetch them via
 * `prompts/list` and `prompts/get`. They appear as slash commands in clients
 * that expose them (Claude Desktop, etc.) and bundle multi-tool sequences
 * into ready-made plans.
 *
 * Design principles:
 *   - One prompt per investigation type (domain / person / brand / infra / link)
 *   - Each prompt names SPECIFIC tools so the agent picks the right primitive
 *   - Each prompt sequences calls in dependency order (resolve before enrich)
 *   - Each prompt explains WHY each step matters so the agent can adapt
 */

const userText = (text: string) => ({
  role: "user" as const,
  content: { type: "text" as const, text },
});

export function registerPrompts(server: McpServer): void {
  // ─────────────────────────────────────────────────────────────────────────
  server.registerPrompt(
    "investigate_domain",
    {
      title: "Investigate a domain (full recon playbook)",
      description:
        "Comprehensive domain investigation. Sequences DNS/CT/subdomain enumeration → infrastructure fingerprinting → API/endpoint discovery → tracker/operator-binding extraction. Use when you have a domain and want to understand: who owns it, what's running on it, what's exposed, and what other assets are linked to the same operator.",
      argsSchema: { target: z.string().describe("Apex domain to investigate, e.g. 'vurvey.app'") },
    },
    ({ target }) => ({
      messages: [
        userText(
`Run a full domain-recon investigation on **${target}**. Execute these phases in order, summarizing findings between phases:

**Phase 1 — Footprint** (parallel):
- \`whois_lookup\` for registration history
- \`dns_lookup\` for the canonical DNS view
- \`cert_transparency\` for the historical SAN list (often surfaces forgotten subdomains)
- \`subfinder_enum\` for active-source subdomain enumeration

**Phase 2 — Asset enrichment** (parallel, on the subdomains found in Phase 1):
- \`http_probe\` to find live HTTP services
- \`tech_stack\` (Wappalyzer-style) for software fingerprints
- \`shodan_internetdb\` on each resolved IP for ports/CPEs/CVEs (free, no key)
- \`favicon_pivot\` for cross-platform infrastructure correlation

**Phase 3 — Exposed surface** (parallel):
- \`exposed_assets\` for misconfigured S3/GCS/Azure blobs
- \`git_secrets\` for leaked GitHub creds
- \`takeover_check\` for dangling subdomain takeovers

**Phase 4 — API surface discovery** (the moat — these all auto-feed \`api_endpoint_record\`):
- \`js_endpoint_extract\` — extract API URLs from JS bundles (LinkFinder-class)
- \`swagger_openapi_finder\` — probe ~35 well-known spec paths
- \`graphql_introspection\` (try first) → \`graphql_clairvoyance\` (fallback when locked)
- \`wayback_endpoint_extract\` — historical archive may expose forgotten endpoints
- \`alienvault_otx_passive_dns\` — passive DNS shadow (4th moat channel; needs OTX_API_KEY)

**Phase 5 — Operator binding** (the SOTA "connect-the-dots" step):
- \`tracker_extract\` on the homepage and 2-3 key subdomains
- Strong tracker IDs (GA UA-/G-, GTM-, FB pixel) = same-operator fingerprint
- Use these IDs to pivot via \`urlscan_search\` for sister sites

**Phase 6 — Synthesis**:
- Query the moat: \`api_endpoint_lookup\` to see what we know cumulatively
- Summarize: who owns it (registrant + tracker IDs), what's running (CPE summary), what's exposed (CVEs + leaks), how many endpoints catalogued, lateral pivots discovered.

Stop early if a phase produces no signal.`,
        ),
      ],
    }),
  );

  // ─────────────────────────────────────────────────────────────────────────
  server.registerPrompt(
    "find_person",
    {
      title: "Find a person across the open web",
      description:
        "Multi-source person lookup. Sequences username/email enumeration across 3000+ sites → social platform discovery → email-breach correlation → entity-graph traversal. Use when you have a name, email, or username and need to find online presence + verified affiliations.",
      argsSchema: {
        query: z.string().describe("Person identifier: full name, email, or username"),
        company: z.string().optional().describe("Known employer if any (improves entity_link_finder accuracy)"),
      },
    },
    ({ query, company }) => ({
      messages: [
        userText(
`Find online presence and connections for **${query}**${company ? ` (known affiliation: ${company})` : ""}. Run these phases in parallel where independent:

**Phase 1 — Username/email enumeration** (parallel — fast):
- \`maigret_search\` — 3000+ sites (most comprehensive)
- \`sherlock_search\` — 400+ sites (faster, less comprehensive)
- \`holehe_check\` — 120+ sites that leak account-existence on /forgot-password

**Phase 2 — Email-specific** (if input is an email):
- \`hibp_check\` — Have I Been Pwned breach correlation
- \`gravatar_lookup\` — public profile metadata
- \`hunter_io_verify\` — email validity + company linkage

**Phase 3 — Federated/social discovery** (parallel):
- \`mastodon_search\`, \`bluesky_search\`, \`keybase_lookup\` — federated identity (5-instance fallback baked into mastodon)
- \`hackernews_user\`, \`stackexchange_user\`, \`reddit_user\` — long-tail tech presence
- \`github_user\`, \`github_emails\` — coding identity + commit-leaked emails

**Phase 4 — Synthesis**:
- \`person_aggregate\` — meta-tool that fans out across the above and unifies results
- If a clear primary identity emerges, use \`entity_link_finder\` with the company name to find shared employers/board seats/co-founders via Diffbot KG

**Phase 5 — Reporting**:
List confirmed accounts (high-confidence: matching profile photo + bio + linked accounts), suspected matches (single-source, name-only), and dead ends. Flag any breach exposure.`,
        ),
      ],
    }),
  );

  // ─────────────────────────────────────────────────────────────────────────
  server.registerPrompt(
    "trace_phishing_campaign",
    {
      title: "Trace a phishing/typosquat campaign against a brand",
      description:
        "Defensive brand-protection playbook. Generates lookalike domains via 9 dnstwist algorithms, filters to registered+MX-present (high-threat), then chains favicon/tracker correlation to confirm whether each lookalike is impersonating the brand. Use when you suspect phishing OR want continuous brand monitoring.",
      argsSchema: { brand_domain: z.string().describe("Real brand domain, e.g. 'anthropic.com'") },
    },
    ({ brand_domain }) => ({
      messages: [
        userText(
`Trace lookalike/phishing infrastructure targeting **${brand_domain}**:

**Phase 1 — Generation** (one call):
- \`typosquat_scan\` with target=${brand_domain}, max_candidates=2000
  → 9 algorithms (omission/transposition/repetition/qwerty-neighbor/bitsquat/IDN-homoglyph/TLD-swap/hyphen/subdomain-split)
  → returns only domains that ALREADY RESOLVE in DNS (cuts noise massively)

**Phase 2 — Threat triage** — focus only on these high-threat subsets:
- \`mx_present=true\` (can receive email — primary phishing signal)
- \`idn=true\` (homoglyph/IDN — visually deceptive)

**Phase 3 — Confirmation per high-threat candidate** (parallel):
- \`favicon_pivot\` on each → compare \`hash_mmh3_fofa\` to ${brand_domain}'s real favicon. Match = high-confidence phishing.
- \`tracker_extract\` on each → look for the brand's real GA/GTM/FB pixel IDs (rare but devastating — means attacker copied JS verbatim)
- \`http_probe\` for redirect chains, certificate issuer, SSL fingerprint
- \`shodan_internetdb\` on the resolved IPs for honeypot tags / known-bad CPEs

**Phase 4 — Pivot for adjacent infrastructure**:
- \`urlscan_search\` on confirmed phishing domains' IPs/ASNs → find sibling phishing pages
- \`cert_transparency\` on confirmed → may reveal additional subdomain phishing under the same operator

**Phase 5 — Output**:
For each confirmed lookalike: domain, method, MX status, favicon-match, tracker-match, IP, ASN, hosting org. Rank by confidence (favicon-match + tracker-match = critical; MX-only = monitor).`,
        ),
      ],
    }),
  );

  // ─────────────────────────────────────────────────────────────────────────
  server.registerPrompt(
    "map_company_infrastructure",
    {
      title: "Map a company's full internet infrastructure footprint",
      description:
        "Maps an organization's IP/ASN footprint, all hosted services, and shadow infrastructure (passive DNS observations the org didn't intend to expose). Use for due-diligence, attack-surface management, or competitive intelligence.",
      argsSchema: { domain: z.string().describe("Apex domain of the target org") },
    },
    ({ domain }) => ({
      messages: [
        userText(
`Map **${domain}**'s complete infrastructure footprint:

**Phase 1 — Discovery surface (advertised)**:
- \`subfinder_enum\` + \`cert_transparency\` — all subdomains the org has put in DNS or CT
- \`whois_lookup\` — registration metadata + nameserver hints

**Phase 2 — Discovery surface (shadow)**:
- \`alienvault_otx_passive_dns\` — passive sensors observed traffic the org may not advertise (internal tooling, legacy IPs)
- \`wayback_endpoint_extract\` — historical: subdomains/endpoints that EXISTED but may be retired

**Phase 3 — IP/ASN mapping**:
- For every distinct A record across all subdomains, run \`asn_lookup\`
- Cluster by ASN — same ASN = same hosting provider; same /24 = likely same physical infra
- For each unique IP: \`shodan_internetdb\` (free port/CPE/CVE fingerprint)

**Phase 4 — Service fingerprint**:
- \`tech_stack\` on key subdomains (api., admin., staging., dashboard., app.)
- \`http_probe\` for live services + their tech banners

**Phase 5 — Operator-correlation across subdomains**:
- \`tracker_extract\` on www, app, dashboard
- \`favicon_pivot\` on each — same favicon hash across all = same internal team
- Note: discrepancies between subdomains (different GA, different favicon) often reveal acquisitions or 3rd-party hosted apps

**Phase 6 — Synthesis**:
- Total subdomain count, unique IP count, unique ASN count
- Hosting-provider breakdown (Cloudflare-fronted vs origin-exposed)
- CVE rollup from Shodan InternetDB
- Notable findings: staging/admin endpoints exposed, deprecated services still live, unusual tracker fingerprints suggesting subsidiary brands.`,
        ),
      ],
    }),
  );

  // ─────────────────────────────────────────────────────────────────────────
  server.registerPrompt(
    "connect_two_entities",
    {
      title: "Find connections between two entities (people or organizations)",
      description:
        "Marquee 'connect-the-dots' playbook. Combines Diffbot KG common-neighbor pathfinding (entity_link_finder), shared-tracker-ID correlation, shared-infrastructure overlap, and breach-overlap signals. Use when answering 'are X and Y connected, and how?' for ER, journalism, or due-diligence.",
      argsSchema: {
        entity_a: z.string().describe("First entity (person or organization)"),
        entity_b: z.string().describe("Second entity"),
      },
    },
    ({ entity_a, entity_b }) => ({
      messages: [
        userText(
`Find every plausible connection between **${entity_a}** and **${entity_b}**. Run these in parallel and merge results:

**Channel 1 — Knowledge graph (curated facts)**:
- \`entity_link_finder\` — Diffbot KG 1-hop common neighbors: shared employers, schools, board seats, co-founded orgs
- Best for: well-indexed people/companies (Forbes-tier or larger)

**Channel 2 — Operator-binding via web fingerprints** (only if entities are/own websites):
- \`tracker_extract\` on each entity's primary domain
- Compare GA/GTM/FB pixel IDs — match = same operator (near-certain ownership link)
- \`favicon_pivot\` on each — same mmh3 hash across both = same internal team

**Channel 3 — Shared infrastructure**:
- \`alienvault_otx_passive_dns\` for both (if domains)
- Cross-reference IPs — overlap = co-hosted (could be coincidence on shared CDN, but worth flagging)
- \`shodan_internetdb\` on overlapping IPs to confirm

**Channel 4 — Breach co-occurrence** (if entities are people/emails):
- \`hibp_check\` for both
- Same breach + same year + similar account-creation pattern = correlated digital identity

**Channel 5 — Public correspondence**:
- \`google_dork_search\` "${entity_a}" + "${entity_b}" — find pages co-mentioning them
- \`hackernews_user\` + \`reddit_user\` — comment threads where both interact
- \`sec_edgar_search\` for ${entity_a} and ${entity_b} — SEC filings cross-reference

**Synthesis**:
Rank connections by:
- **Strong**: shared org membership in KG + tracker-ID match + same-month breach overlap
- **Medium**: KG only, or tracker-ID only
- **Weak**: same-IP co-hosting (could be CDN coincidence), occasional co-mentioning in articles
Output a connection graph with edge confidence.`,
        ),
      ],
    }),
  );
}
