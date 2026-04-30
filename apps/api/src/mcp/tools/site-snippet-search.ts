import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const presets = [
  "linkedin", "linkedin_company", "zoominfo", "rocketreach", "glassdoor",
  "newspapers", "ancestry", "beenverified", "fastpeoplesearch", "spokeo",
  "radaris", "thatsthem", "peekyou", "truepeoplesearch",
  "instagram", "tiktok", "twitter", "facebook",
] as const;

const input = z.object({
  preset: z.enum(presets).optional().describe("Named site preset with built-in snippet parser. Use 'linkedin' for /in/ profiles, 'zoominfo' or 'rocketreach' for B2B contacts, 'glassdoor' for salaries, 'newspapers'/'ancestry' for paywalled genealogy/historical archives indexed by Google."),
  site_domain: z.string().optional().describe("Arbitrary domain (e.g. 'pinterest.com', 'medium.com') if no preset fits — returns raw snippets without site-specific parsing"),
  query: z.string().min(2).describe("Search query (e.g. person name + city, employer name, etc.)"),
  limit: z.number().int().min(1).max(30).default(10),
}).refine((d) => d.preset || d.site_domain, { message: "Either preset or site_domain is required" });

toolRegistry.register({
  name: "site_snippet_search",
  description:
    "**Generalized Tavily-bypass for any Cloudflare-blocked / anti-bot / paywalled site** — exploits the fact that Google's crawler has special access while humans + bots get blocked. Searches Tavily (or Firecrawl fallback) for `site:DOMAIN query` and parses the result snippets, which contain the same structured data the live page would. Unlocks 11+ previously-blocked OSINT surfaces: LinkedIn personal+company profiles, ZoomInfo B2B contacts, RocketReach contacts, Glassdoor employer salaries, paywalled Newspapers.com obituaries, Ancestry.com family trees, BeenVerified, Instagram/TikTok/Twitter public profiles, Spokeo/FastPeopleSearch/Radaris/ThatsThem/PeekYou people-search aggregators. Built-in site presets have specific parsers: LinkedIn extracts job_title + employer + location + education; ZoomInfo extracts name + role + company; Glassdoor extracts salary; Instagram extracts follower/post counts. For arbitrary sites pass site_domain. REQUIRES TAVILY_API_KEY (or FIRECRAWL_API_KEY fallback).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "site_snippet_search", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "site_snippet_search failed");
    return res.output;
  },
});
