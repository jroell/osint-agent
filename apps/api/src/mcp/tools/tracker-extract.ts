import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().optional().describe("URL to scan for tracking IDs (e.g. 'https://vurvey.app')"),
  html: z.string().optional().describe("Pre-rendered HTML — bypass the URL fetch. Useful when chaining from firecrawl_scrape on SPAs."),
}).refine(d => d.url || d.html, { message: "url or html required" });

toolRegistry.register({
  name: "tracker_extract",
  description:
    "**SOTA 'connecting the dots' primitive — extracts tracking IDs that bind a domain to its OPERATOR.** Pulls Google Analytics (UA-/G-/GTM-), Facebook Pixel, Hotjar, Mixpanel, Segment, Stripe publishable keys, LinkedIn Insight, Twitter/X Pixel, Pinterest Tag, TikTok Pixel, Klaviyo, Intercom, Drift, FullStory, Amplitude, Heap, HubSpot, Shopify, Yandex Metrica, VK Pixel IDs from any URL. Same tracker ID across two domains = nearly-certain shared operator (the strongest non-DNS/WHOIS identity binding in OSINT). Used by Bellingcat to map disinfo networks. Returns ID-strength rating (strong/medium/weak) + pivot hints for chaining into urlscan_search/publicwww. Works on any HTTP(S) URL — no API keys, no rate limits.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tracker_extract",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tracker_extract failed");
    return res.output;
  },
});
