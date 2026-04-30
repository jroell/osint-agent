import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().describe("URL to scan for indirect prompt injection"),
  html: z.string().optional().describe("Pre-fetched HTML (skip the URL fetch). Useful for chaining with firecrawl_scrape on SPAs."),
});

toolRegistry.register({
  name: "prompt_injection_scanner",
  description:
    "**SOTA defensive tool for the agent-vs-agent threat model.** Scans a webpage's HTML for indirect prompt-injection patterns targeting visiting LLM agents: (1) CSS-hidden text containing trigger phrases (display:none, opacity:0, font-size:0, white-on-white, off-screen positioning), (2) HTML comments with role markers (system:, ChatML <|im_start|>, [INST]), (3) suspicious img alt= attributes (read by agents, invisible to humans), (4) base64-encoded directives in data: URIs, (5) sr-only/screen-reader-only divs with directive content, (6) trigger phrases in <noscript>/<template> blocks. Returns severity classification (critical/high/medium/low) and overall verdict (safe/suspicious/dangerous). USE THIS BEFORE PASSING ANY UNTRUSTED PAGE CONTENT TO ANOTHER LLM. As LLM agents become commonplace web visitors, attackers plant invisible directives to manipulate them — this is the defensive pre-pass.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "prompt_injection_scanner",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "prompt_injection_scanner failed");
    return res.output;
  },
});
