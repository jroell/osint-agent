import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["render_url", "screenshot", "network_log"]).optional(),
  url: z.string().url().describe("URL to render."),
});

toolRegistry.register({
  name: "browserbase_session",
  description:
    "**Browserbase headless-browser session — REQUIRES BROWSERBASE_API_KEY and BROWSERBASE_PROJECT_ID (free tier at browserbase.com).** Creates a real Chrome session and returns the connect URL + Selenium URL for downstream CDP automation. Use when Firecrawl can't render JS-heavy pages or simple anti-bot blocks rule-based scraping. Each output emits typed entity envelope (kind: rendered_page) with session metadata. Complements `firecrawl_scrape` (free) and `scrapingbee_fetch`.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "browserbase_session",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "browserbase_session failed");
    return res.output;
  },
});
