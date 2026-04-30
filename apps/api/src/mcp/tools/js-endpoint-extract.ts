import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().describe("Target site URL — JS bundles will be auto-discovered from <script> tags"),
  max_js_files: z.number().int().min(1).max(200).default(30),
  concurrency: z.number().int().min(1).max(20).default(6),
  include_potential_secrets: z.boolean().default(true)
    .describe("Surface strings matching known leaked-credential patterns (AWS keys, OpenAI sk-, GitHub PATs, Slack tokens, Google API keys, Firebase configs)"),
});

toolRegistry.register({
  name: "js_endpoint_extract",
  description:
    "**API-discovery primitive.** Fetches a target site's HTML, extracts every <script src> bundle + inline scripts, then runs LinkFinder-class regex extraction across each JS file to surface API endpoints, GraphQL operations, subdomain references, and potential leaked credentials. Modern web apps ship their entire API surface (base URLs, route maps, GraphQL schemas, sometimes auth tokens) directly into client JS bundles — this tool reverses-out that exposure. Each extracted URL gets an `api_score` (0–10) flagging high-likelihood API endpoints vs. static assets. Pipe results into http_probe / stealth_http_fetch to verify auth posture per endpoint.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "js_endpoint_extract",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "js_endpoint_extract failed");
    return res.output;
  },
});
