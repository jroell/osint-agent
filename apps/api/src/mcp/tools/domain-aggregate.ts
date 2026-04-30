import { z } from "zod";
import { toolRegistry } from "./instance";
import type { AuthContext } from "../../auth/middleware";

const input = z.object({
  domain: z.string().min(3).describe("Bare domain (e.g. 'vurvey.app') — apex only, the tool handles subdomain enumeration"),
  include_active_probing: z.boolean().default(false)
    .describe("If true, includes port_scan_passive against the apex IP. Only set true if you have explicit authorization to probe the target."),
  per_tool_timeout_ms: z.number().int().min(2000).max(60000).default(15_000),
});

toolRegistry.register({
  name: "domain_aggregate",
  description:
    "Meta-tool: full-spectrum domain reconnaissance in a single call. Fans out (with bounded concurrency) to all domain-relevant tools — whois, DNS, ASN, cert transparency, subfinder, http_probe, tech_stack, takeover_check, exposed_asset_find, wayback, common_crawl, theHarvester, Diffbot KG (Organization), Tavily/Perplexity (synthesis). Returns a unified domain dossier with attack surface, tech stack, brand mentions, and corporate metadata.",
  inputSchema: input,
  costMillicredits: 50,
  handler: async (i, ctx) => {
    const t0 = Date.now();
    const { domain, include_active_probing, per_tool_timeout_ms } = i;

    type Plan = { tool: string; args: Record<string, unknown>; gate: string };
    const plan: Plan[] = [
      // Identity / DNS / hosting
      { tool: "whois_query", args: { target: domain }, gate: "identity" },
      { tool: "dns_lookup_comprehensive", args: { domain }, gate: "identity" },
      // Subdomain enumeration (multi-source)
      { tool: "cert_transparency_query", args: { domain }, gate: "subdomain-enum" },
      { tool: "subfinder_passive", args: { domain }, gate: "subdomain-enum" },
      { tool: "theharvester", args: { domain, limit: 100, timeout_seconds: 60 }, gate: "subdomain-enum" },
      // Apex fingerprint
      { tool: "http_probe", args: { url: `https://${domain}` }, gate: "fingerprint" },
      { tool: "tech_stack_fingerprint", args: { url: `https://${domain}` }, gate: "fingerprint" },
      // Takeover screen on apex
      { tool: "takeover_check", args: { domain }, gate: "posture" },
      // Cloud assets
      { tool: "exposed_asset_find", args: { target: domain.split(".")[0] }, gate: "posture" },
      // Historical
      { tool: "wayback_history", args: { url: domain, match_type: "domain", limit: 100 }, gate: "historical" },
      { tool: "common_crawl_lookup", args: { url: domain, limit: 20 }, gate: "historical" },
      // Corporate / brand / synthesis
      { tool: "diffbot_kg_query", args: { query: `type:Organization homepageUri:"${domain}" OR homepageUri:"https://${domain}" OR homepageUri:"https://www.${domain}"`, size: 5 }, gate: "corporate" },
      { tool: "tavily_search", args: { query: `What is the company behind ${domain}? Founders, employees, funding, headquarters.`, search_depth: "advanced", limit: 6, include_answer: true }, gate: "synthesis" },
      { tool: "perplexity_search", args: { query: `Tell me about the company at ${domain}. What do they do, when were they founded, who are the founders?` }, gate: "synthesis" },
      { tool: "reddit_query", args: { mode: "search", query: domain.split(".")[0], limit: 8 }, gate: "synthesis" },
      // Email enumeration if Hunter.io key is present (gracefully fails if not)
      { tool: "hunter_io_email_finder", args: { domain, limit: 25 }, gate: "people" },
    ];

    if (include_active_probing) {
      // Active scan only when explicitly requested.
      plan.push({ tool: "port_scan_passive", args: { target: domain }, gate: "active" });
    }

    const concurrency = 4;
    const results: Array<{ tool: string; gate: string; took_ms: number; output?: any; error?: string }> = new Array(plan.length);
    let next = 0;
    await Promise.all(Array(Math.min(concurrency, plan.length)).fill(0).map(async () => {
      while (next < plan.length) {
        const idx = next++;
        const p = plan[idx]!;
        const start = Date.now();
        try {
          const result = await Promise.race([
            toolRegistry.invoke(p.tool, p.args, ctx as AuthContext),
            new Promise((_, reject) => setTimeout(() => reject(new Error(`per-tool timeout (${per_tool_timeout_ms}ms)`)), per_tool_timeout_ms)),
          ]);
          results[idx] = { tool: p.tool, gate: p.gate, took_ms: Date.now() - start, output: result };
        } catch (e: any) {
          results[idx] = { tool: p.tool, gate: p.gate, took_ms: Date.now() - start, error: (e?.message || String(e)).slice(0, 200) };
        }
      }
    }));

    const get = (toolName: string) => results.find(r => r.tool === toolName && !r.error)?.output as any;

    // Aggregate subdomain union across multi-source enumerators.
    const allSubs = new Set<string>([domain]);
    for (const tool of ["cert_transparency_query", "subfinder_passive", "theharvester"]) {
      const o = get(tool);
      const list: string[] = (o?.subdomains || o?.hosts || []) as string[];
      for (const s of list) {
        const norm = s.toLowerCase().trim().replace(/^\*\./, "");
        if (norm.endsWith(domain) || norm === domain) allSubs.add(norm);
      }
    }

    const dossier: any = {
      target: domain,
      identity: get("whois_query") ? {
        registrar: get("whois_query").registrar,
        created: get("whois_query").created,
        expires: get("whois_query").expires,
        nameservers: get("whois_query").nameservers,
      } : null,
      apex_dns: get("dns_lookup_comprehensive") ? {
        a: get("dns_lookup_comprehensive").a,
        aaaa: get("dns_lookup_comprehensive").aaaa,
        ns: get("dns_lookup_comprehensive").ns,
        mx: get("dns_lookup_comprehensive").mx,
        txt_count: get("dns_lookup_comprehensive").txt?.length || 0,
        txt_signals: (get("dns_lookup_comprehensive").txt || []).filter((t: string) => /^v=|verification|firebase=|amazonses/.test(t)),
      } : null,
      attack_surface: {
        subdomains_discovered: allSubs.size,
        subdomain_sources: Object.fromEntries(["cert_transparency_query", "subfinder_passive", "theharvester"].map(t => [
          t, get(t)?.count ?? get(t)?.subdomains?.length ?? get(t)?.hosts?.length ?? 0,
        ])),
        ips: get("dns_lookup_comprehensive")?.a || [],
      },
      apex_fingerprint: get("http_probe") ? {
        status: get("http_probe").status,
        title: get("http_probe").title,
        server: get("http_probe").server,
        favicon_md5: get("http_probe").favicon_md5,
        tls_issuer: get("http_probe").tls_issuer,
        technologies: Object.keys(get("tech_stack_fingerprint")?.technologies || {}),
        categories: get("tech_stack_fingerprint")?.categories || [],
      } : null,
      posture: {
        takeover_findings: get("takeover_check")?.findings?.length || 0,
        takeover_vulnerable: (get("takeover_check")?.findings || []).filter((f: any) => f.vulnerable).length,
        cloud_buckets_found: get("exposed_asset_find")?.hits?.length || 0,
        public_buckets: (get("exposed_asset_find")?.hits || []).filter((h: any) => h.permission === "public-read" || h.permission === "public-list").length,
      },
      historical: {
        wayback_first: get("wayback_history")?.first_seen,
        wayback_last: get("wayback_history")?.last_seen,
        wayback_count: get("wayback_history")?.count,
        common_crawl_hits: get("common_crawl_lookup")?.count,
      },
      corporate: get("diffbot_kg_query") ? {
        kg_entities: (get("diffbot_kg_query").entities || []).slice(0, 3).map((e: any) => ({
          id: e.id, name: e.name,
          description: typeof e.description === "string" ? e.description.slice(0, 200) : undefined,
          founded: e.foundingDate?.timestamp ? new Date(e.foundingDate.timestamp).toISOString().slice(0, 10) : e.foundingDate,
          location: e.location?.city?.name,
          founders: (e.founders || []).slice(0, 5).map((f: any) => f.name),
          employees: e.nbEmployees || e.nbEmployeesMax,
          industry: (e.industries || []).slice(0, 3).map((i: any) => i.name),
          twitter: (e.allUris || []).find((u: string) => /twitter\.com|x\.com/.test(u)),
          linkedin: (e.allUris || []).find((u: string) => /linkedin\.com/.test(u)),
          crunchbase: (e.allUris || []).find((u: string) => /crunchbase\.com/.test(u)),
          wikipedia: e.wikipediaUri,
        })),
      } : null,
      synthesis: {
        tavily: get("tavily_search") ? { answer: get("tavily_search").answer, sources: (get("tavily_search").results || []).map((x: any) => x.url).slice(0, 5) } : null,
        perplexity: get("perplexity_search") ? { answer: get("perplexity_search").answer, citations: get("perplexity_search").citations } : null,
      },
      brand_footprint: {
        reddit_mentions: get("reddit_query")?.count,
        reddit_top: (get("reddit_query")?.items || []).slice(0, 3).map((r: any) => ({ subreddit: r.subreddit, title: r.title, score: r.score })),
      },
      people: get("hunter_io_email_finder") ? {
        organization: get("hunter_io_email_finder").organization,
        pattern: get("hunter_io_email_finder").pattern,
        emails_found: get("hunter_io_email_finder").emails_found,
      } : null,
      port_scan: include_active_probing ? get("port_scan_passive") : null,
      per_tool_breakdown: results.map(r => ({ tool: r.tool, gate: r.gate, took_ms: r.took_ms, ok: !r.error, error: r.error })),
      tools_succeeded: results.filter(r => !r.error).length,
      tools_errored: results.filter(r => r.error).length,
      took_ms: Date.now() - t0,
    };

    return dossier;
  },
});
