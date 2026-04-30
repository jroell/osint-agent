import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url(),
  proxy_mode: z.enum(["basic", "premium", "stealth"]).default("basic").describe("'basic' = 1 credit/req, 'premium' = 10-25 credits (datacenter rotation), 'stealth' = 75 credits (residential IPs + browser fingerprint masking — for very-aggressive defense tier)"),
  render_js: z.boolean().default(true).describe("Whether to execute JS in headless Chromium"),
  country_code: z.string().length(2).optional().describe("ISO 2-letter country code for proxy egress (e.g. 'us', 'gb', 'de')"),
});

toolRegistry.register({
  name: "scrapingbee_fetch",
  description:
    "**ScrapingBee fallback bypass for sites Firecrawl can't reach** — different proxy network (residential IPs in 60+ countries) + Chromium rendering. Some sites that reject Firecrawl's IP ranges may pass through ScrapingBee cleanly (and vice versa) — different ASN footprint = different anti-bot fingerprint at network layer. Three modes: basic (cheap, datacenter), premium (10-25 credits, rotating datacenter), stealth (75 credits, residential + fingerprint masking). Returns full HTML, status, response headers (incl Cf-Ray for CF-detection), captcha/JS-required indicators in highlights. Use this when firecrawl_extract or firecrawl_map fail with status 5xx/captcha. REQUIRES SCRAPING_BEE_API_KEY.",
  inputSchema: input,
  costMillicredits: 6,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "scrapingbee_fetch", input: i, timeoutMs: 200_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "scrapingbee_fetch failed");
    return res.output;
  },
});
