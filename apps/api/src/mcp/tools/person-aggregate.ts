import { z } from "zod";
import { toolRegistry } from "./instance";
import type { AuthContext } from "../../auth/middleware";

const input = z.object({
  name: z.string().optional().describe("Person's display name (used by stackexchange, mastodon, opensanctions)"),
  email: z.string().email().optional().describe("Known email (used by gravatar, holehe, hibp)"),
  username: z.string().optional().describe("Known handle/username (used by hn, bluesky, keybase, github, mastodon, sherlock)"),
  domain: z.string().optional().describe("Corporate domain (used by hunter_io if HUNTER_IO_API_KEY set)"),
  include_heuristic: z.boolean().default(false)
    .describe("Include sherlock-family heuristic searches. False (default) keeps results high-precision; true broadens coverage at the cost of more false positives."),
  per_tool_timeout_ms: z.number().int().min(2000).max(60000).default(15_000),
});

type Identity = {
  platform: string;
  handle?: string;
  url?: string;
  display_name?: string;
  verified?: boolean;        // cryptographically signed (Keybase) or platform-verified
  source_tool: string;
  evidence?: Record<string, unknown>;
};

type EmailFinding = {
  email: string;
  source_tool: string;
  evidence?: Record<string, unknown>;
};

toolRegistry.register({
  name: "person_aggregate",
  description:
    "Meta-tool: fans out to every people-finder tool in parallel based on which inputs are supplied (name/email/username/domain), aggregates results into a unified identity dossier, and deduplicates handles seen across multiple platforms. Single tool call replaces 10+ individual lookups. Returns: identities found across platforms, emails discovered, cross-platform handle clusters (same string used as handle on N platforms), and a per-tool timing/error breakdown for transparency.",
  inputSchema: input,
  costMillicredits: 25,
  handler: async (i, ctx) => {
    const t0 = Date.now();
    const { name, email, username, domain, include_heuristic, per_tool_timeout_ms } = i;
    if (!name && !email && !username && !domain) {
      throw new Error("at least one of name / email / username / domain must be provided");
    }

    // Build the dispatch plan — each entry runs in parallel.
    type Plan = { tool: string; args: Record<string, unknown>; gate: string };
    const plan: Plan[] = [];

    if (username) {
      plan.push({ tool: "hackernews_user", args: { username, include_recent: false }, gate: "username" });
      plan.push({ tool: "bluesky_user", args: { handle: username, include_recent: false }, gate: "username" });
      plan.push({ tool: "keybase_lookup", args: { username }, gate: "username" });
      plan.push({ tool: "github_user_profile", args: { login: username }, gate: "username" });
      plan.push({ tool: "github_commit_emails", args: { login: username, pages: 2 }, gate: "username" });
      plan.push({ tool: "stackexchange_user", args: { query: username, limit_per_site: 3 }, gate: "username" });
      plan.push({ tool: "mastodon_user_lookup", args: { query: username, per_instance_timeout_s: 5 }, gate: "username" });
      // Live X content via Grok — only credible path since snscrape died.
      plan.push({ tool: "grok_x_search", args: { query: `Tell me about @${username} on X. Profile bio, recent posts, notable activity. Be specific and cite tweets.` }, gate: "username" });
      if (include_heuristic) {
        plan.push({ tool: "username_search_sherlock", args: { username, timeout_seconds: 30 }, gate: "username+heuristic" });
      }
    }

    if (email) {
      plan.push({ tool: "gravatar_lookup", args: { email }, gate: "email" });
      plan.push({ tool: "email_holehe", args: { email, timeout_seconds: 25 }, gate: "email" });
      plan.push({ tool: "hibp_breach_lookup", args: { email }, gate: "email" });
    }

    if (name) {
      plan.push({ tool: "stackexchange_user", args: { query: name, limit_per_site: 2 }, gate: "name" });
      plan.push({ tool: "mastodon_user_lookup", args: { query: name, per_instance_timeout_s: 5 }, gate: "name" });
      plan.push({ tool: "opensanctions_screen", args: { name }, gate: "name" });
      // Diffbot Knowledge Graph — highest-precision people enrichment.
      plan.push({ tool: "diffbot_kg_query", args: { query: `type:Person name:"${name}"`, size: 5 }, gate: "name" });
      // Tavily synthesizes "who is X" with citations.
      plan.push({ tool: "tavily_search", args: { query: `Who is ${name}? Background, current role, employer, education, family.`, search_depth: "advanced", limit: 6, include_answer: true }, gate: "name" });
      // Perplexity — citation-grounded answer for current employment.
      plan.push({ tool: "perplexity_search", args: { query: `Who is ${name}? What is their current employer and role?` }, gate: "name" });
      // Google dorks for LinkedIn presence.
      plan.push({ tool: "google_dork_search", args: { query: `site:linkedin.com/in/ "${name}"`, limit: 8 }, gate: "name" });
    }

    if (domain) {
      plan.push({ tool: "hunter_io_email_finder", args: { domain }, gate: "domain" });
      plan.push({ tool: "diffbot_kg_query", args: { query: `type:Organization homepageUri:"${domain}"`, size: 3 }, gate: "domain" });
    }

    // De-duplicate plan: same tool+args shouldn't be run twice (e.g. when both name and username
    // happen to equal the same string and both trigger stackexchange_user).
    const seen = new Set<string>();
    const dedupedPlan = plan.filter((p) => {
      const key = `${p.tool}::${JSON.stringify(p.args)}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });

    // Fan out with bounded concurrency. Bun's localhost HTTP pool chokes on
    // 13+ simultaneous fetches to the Go worker (we hit this race in earlier
    // recon runs); cap to 4 in-flight to keep each call's fetch budget healthy.
    const runOne = async (p: Plan): Promise<{ tool: string; gate: string; took_ms: number; output?: unknown; error?: string }> => {
      const start = Date.now();
      try {
        const result = await Promise.race([
          toolRegistry.invoke(p.tool, p.args, ctx as AuthContext),
          new Promise((_, reject) => setTimeout(() => reject(new Error(`per-tool timeout (${per_tool_timeout_ms}ms)`)), per_tool_timeout_ms)),
        ]);
        return { tool: p.tool, gate: p.gate, took_ms: Date.now() - start, output: result };
      } catch (e) {
        const msg = (e as Error).message;
        const formatted = (!msg || msg === p.tool) ? "(empty error message — likely fetch/connection-pool issue)" : msg.slice(0, 200);
        return { tool: p.tool, gate: p.gate, took_ms: Date.now() - start, error: formatted };
      }
    };

    const concurrency = 4;
    const results: Awaited<ReturnType<typeof runOne>>[] = new Array(dedupedPlan.length);
    let next = 0;
    await Promise.all(Array(Math.min(concurrency, dedupedPlan.length)).fill(0).map(async () => {
      while (next < dedupedPlan.length) {
        const i = next++;
        const item = dedupedPlan[i]!;
        results[i] = await runOne(item);
      }
    }));

    // Normalize results into a unified identity graph.
    const identities: Identity[] = [];
    const emails: EmailFinding[] = [];
    const handleClusters: Record<string, Set<string>> = {}; // handle → platforms

    const recordHandle = (handle: string, platform: string) => {
      const h = handle.toLowerCase();
      (handleClusters[h] ??= new Set()).add(platform);
    };

    for (const r of results) {
      if (r.error || !r.output) continue;
      const o = r.output as any;
      switch (r.tool) {
        case "hackernews_user":
          if (o.id) {
            identities.push({ platform: "hackernews", handle: o.id, url: o.profile_url, source_tool: r.tool,
              evidence: { karma: o.karma, account_age: o.created_at_iso, submissions: o.submitted_count } });
            recordHandle(o.id, "hackernews");
          }
          break;
        case "bluesky_user":
          if (o.handle) {
            identities.push({ platform: "bluesky", handle: o.handle, url: o.profile_url, display_name: o.display_name,
              source_tool: r.tool, evidence: { followers: o.followers_count, posts: o.posts_count, did: o.did } });
            recordHandle(o.handle.split(".")[0], "bluesky");
          }
          break;
        case "keybase_lookup":
          if (o.username) {
            identities.push({ platform: "keybase", handle: o.username, url: o.profile_url, display_name: o.full_name,
              source_tool: r.tool, evidence: { proofs: o.identity_proofs?.length, location: o.location } });
            recordHandle(o.username, "keybase");
            // Keybase proofs are *cryptographically verified* — promote each to its own identity.
            for (const p of (o.identity_proofs || [])) {
              if (p.state !== 1) continue;
              const platform = p.proof_type === "generic_web_site" ? "website" : p.proof_type;
              identities.push({ platform, handle: p.nametag, url: p.service_url || p.human_url,
                verified: true, source_tool: "keybase_lookup",
                evidence: { keybase_state: p.state, proof_type: p.proof_type } });
              if (typeof p.nametag === "string" && !p.nametag.includes(".")) recordHandle(p.nametag, platform);
            }
          }
          break;
        case "github_user_profile":
          if (o.profile?.login) {
            identities.push({ platform: "github", handle: o.profile.login, url: o.profile.html_url,
              display_name: o.profile.name, source_tool: r.tool,
              evidence: { followers: o.profile.followers, repos: o.profile.public_repos,
                          twitter: o.profile.twitter_username, email: o.profile.email,
                          location: o.profile.location, blog: o.profile.blog } });
            recordHandle(o.profile.login, "github");
            if (o.profile.email) emails.push({ email: o.profile.email, source_tool: r.tool, evidence: { profile_field: true } });
            if (o.profile.twitter_username) recordHandle(o.profile.twitter_username, "twitter");
          }
          break;
        case "github_commit_emails":
          for (const hit of (o.emails || [])) {
            emails.push({ email: hit.email, source_tool: r.tool,
              evidence: { commits: hit.commit_count, repos: hit.repos_seen, names: hit.names_seen, noreply: hit.github_noreply } });
          }
          break;
        case "stackexchange_user":
          for (const u of (o.users || [])) {
            identities.push({ platform: `stackexchange:${u.site}`, handle: String(u.user_id), url: u.link,
              display_name: u.display_name, source_tool: r.tool,
              evidence: { reputation: u.reputation, location: u.location, website: u.website_url } });
          }
          break;
        case "mastodon_user_lookup":
          for (const m of (o.matches || [])) {
            identities.push({ platform: `mastodon:${m.instance}`, handle: m.acct, url: m.url,
              display_name: m.display_name, source_tool: r.tool,
              evidence: { followers: m.followers_count, statuses: m.statuses_count, bot: m.bot } });
            recordHandle(m.username, `mastodon:${m.instance}`);
          }
          break;
        case "gravatar_lookup":
          if (o.has_gravatar) {
            identities.push({ platform: "gravatar", url: o.profile_url, display_name: o.display_name,
              source_tool: r.tool, evidence: { location: o.location, job_title: o.job_title } });
            for (const va of (o.verified_accounts || [])) {
              identities.push({ platform: va.domain, handle: va.username, url: va.url,
                verified: va.verified, source_tool: "gravatar_lookup" });
              if (va.username) recordHandle(va.username, va.domain);
            }
          }
          break;
        case "email_holehe":
          for (const h of (o.confirmed || [])) {
            identities.push({ platform: h.site, source_tool: r.tool,
              evidence: { ...(h.others || {}), holehe_confirmed: true, phone: h.phone, fullname: h.fullname } });
          }
          break;
        case "hibp_breach_lookup":
          if (o.pwned) {
            identities.push({ platform: "hibp", source_tool: r.tool,
              evidence: { breach_count: o.count, breaches: (o.breaches || []).map((b: any) => b.Name) } });
          }
          break;
        case "username_search_sherlock":
          for (const f of (o.found || [])) {
            identities.push({ platform: `sherlock:${f.site}`, url: f.url, source_tool: r.tool,
              evidence: { heuristic: true, response_time_ms: f.response_time_ms } });
          }
          break;
        case "opensanctions_screen":
          for (const m of (o.results || []).slice(0, 5)) {
            identities.push({ platform: "opensanctions", handle: m.id, display_name: m.caption,
              source_tool: r.tool, evidence: { score: m.score, datasets: m.datasets, topics: m.topics } });
          }
          break;
        case "hunter_io_email_finder":
          for (const e of (o.emails || [])) {
            emails.push({ email: e.value, source_tool: r.tool,
              evidence: { confidence: e.confidence, name: `${e.first_name||""} ${e.last_name||""}`.trim(),
                          position: e.position, linkedin: e.linkedin, twitter: e.twitter } });
          }
          break;
        case "diffbot_kg_query":
          for (const ent of (o.entities || []).slice(0, 5)) {
            const id: Identity = {
              platform: "diffbot_kg",
              handle: ent.id || ent.diffbotUri,
              url: ent.diffbotUri || ent.uri,
              display_name: ent.name,
              verified: true,  // Diffbot KG entries are curated/cross-referenced
              source_tool: r.tool,
              evidence: {
                description: typeof ent.description === "string" ? ent.description.slice(0, 240) : undefined,
                employer: ent.allEmployers?.[0]?.name || ent.employments?.[0]?.employer?.name,
                role: ent.employments?.[0]?.title,
                location: ent.location?.city?.name || ent.locations?.[0]?.city?.name,
                nationality: ent.nationalities?.[0]?.name,
                gender: ent.gender,
                image: ent.image || ent.images?.[0]?.url,
                wikipedia: ent.wikipediaUri,
                linkedin: (ent.allUris || []).find((u: string) => /linkedin\.com/.test(u)),
                twitter: (ent.allUris || []).find((u: string) => /twitter\.com|x\.com/.test(u)),
                github: (ent.allUris || []).find((u: string) => /github\.com/.test(u)),
              },
            };
            identities.push(id);
          }
          break;
        case "tavily_search":
        case "perplexity_search":
        case "grok_x_search":
          if (o.answer) {
            identities.push({
              platform: r.tool === "grok_x_search" ? "x_via_grok" : (r.tool === "tavily_search" ? "tavily_synthesis" : "perplexity_synthesis"),
              source_tool: r.tool,
              evidence: {
                answer: typeof o.answer === "string" ? o.answer.slice(0, 800) : undefined,
                citations: o.citations || (o.results || []).map((x: any) => x.url).slice(0, 8),
                model: o.model,
              },
            });
          }
          break;
        case "google_dork_search":
          for (const r2 of (o.results || []).slice(0, 6)) {
            // Surface LinkedIn-style hits as identities since this is the path
            // person_aggregate uses for `site:linkedin.com/in/` queries.
            if (/linkedin\.com\/in\//.test(r2.url || "")) {
              identities.push({
                platform: "linkedin (via google_dork)",
                url: r2.url,
                display_name: r2.title?.replace(/ \| LinkedIn.*$/i, "").trim(),
                source_tool: r.tool,
                evidence: { snippet: r2.snippet?.slice(0, 200), engine: r2.engine },
              });
            }
          }
          break;
      }
    }

    const crossPlatform: Record<string, string[]> = {};
    for (const [handle, platforms] of Object.entries(handleClusters)) {
      if (platforms.size >= 2) crossPlatform[handle] = [...platforms].sort();
    }

    // Surface the synthesis-tool answers up top so an agent can read them
    // without grepping through the identities array.
    const syntheses: Record<string, unknown> = {};
    for (const r of results) {
      if (r.error || !r.output) continue;
      const o = r.output as any;
      if (r.tool === "tavily_search" && o.answer) syntheses.tavily = { answer: o.answer, sources: (o.results || []).map((x: any) => x.url).slice(0, 5) };
      if (r.tool === "perplexity_search" && o.answer) syntheses.perplexity = { answer: o.answer, citations: o.citations };
      if (r.tool === "grok_x_search" && o.answer) syntheses.grok_x = { answer: o.answer };
    }

    return {
      inputs: { name, email, username, domain, include_heuristic },
      tools_called: dedupedPlan.length,
      tools_succeeded: results.filter(r => !r.error).length,
      tools_errored: results.filter(r => r.error).length,
      identities_found: identities.length,
      identities,
      emails_discovered: emails.length,
      emails,
      cross_platform_handles: crossPlatform,
      synthesis: syntheses,
      per_tool_breakdown: results.map(r => ({
        tool: r.tool, gate: r.gate, took_ms: r.took_ms,
        ok: !r.error, error: r.error,
      })),
      took_ms: Date.now() - t0,
    };
  },
});
