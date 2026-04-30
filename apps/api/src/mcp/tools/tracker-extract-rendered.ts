import { z } from "zod";
import { toolRegistry } from "./instance";

const input = z.object({
  url: z.string().describe("URL to render + scan (e.g. 'https://anthropic.com')"),
  wait_for: z.string().optional().describe("CSS selector to wait for before extracting (default: rely on firecrawl's auto-wait)"),
});

toolRegistry.register({
  name: "tracker_extract_rendered",
  description:
    "**SPA-aware tracker extraction — chains firecrawl_scrape → tracker_extract.** Solves the long-standing limitation where modern SPA-heavy sites (anthropic.com, openai.com, shopify.com) load tracker scripts asynchronously, leaving initial HTML empty. Renders JS via firecrawl_scrape (uses your FIRECRAWL_API_KEY), then feeds the rendered HTML into tracker_extract's regex pipeline. Returns the same tracker IDs + 3rd-party domains + leak severity classification as `tracker_extract` but works on JS-heavy sites. Use when `tracker_extract` returns 0 IDs on a site you know uses analytics. Cost: 1 firecrawl_scrape credit + tracker_extract regex (sub-millisecond).",
  inputSchema: input,
  costMillicredits: 6,
  handler: async (i, ctx) => {
    // Step 1: render via firecrawl_scrape with raw HTML + scripts preserved
    const rendered = await toolRegistry.invoke(
      "firecrawl_scrape",
      { url: i.url, include_html: true, only_main_content: false },
      ctx,
    ) as any;

    const html = rendered?.html ?? rendered?.markdown ?? "";
    if (!html) {
      throw new Error("firecrawl_scrape returned empty HTML — cannot extract trackers from rendered content");
    }

    // Step 2: feed rendered HTML into tracker_extract
    const extracted = await toolRegistry.invoke(
      "tracker_extract",
      { url: i.url, html },
      ctx,
    ) as any;

    return {
      ...extracted,
      rendered_via: "firecrawl_scrape",
      rendered_bytes: html.length,
    };
  },
});
