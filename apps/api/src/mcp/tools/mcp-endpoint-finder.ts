import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().optional().describe("Host or full URL to probe (e.g. 'mcp.example.com'). Required UNLESS direct_url is provided."),
  direct_url: z.string().optional().describe("Skip path probing — interrogate this exact URL as an MCP endpoint"),
}).refine(d => d.target || d.direct_url, { message: "Provide either target or direct_url" });

toolRegistry.register({
  name: "mcp_endpoint_finder",
  description:
    "**SOTA agent-vs-agent reconnaissance — no other OSINT tool does this.** Probes a host for exposed Model Context Protocol servers at ~10 well-known paths (/mcp, /sse, /api/mcp, /.well-known/mcp, etc.), then interrogates any discovered server via JSON-RPC `initialize` + `tools/list` + `prompts/list` + `resources/list` to enumerate full agent capabilities. Auto-classifies severity: CRITICAL = no-auth public server with destructive tools (delete_/send_/execute_) = data destruction risk; HIGH = no-auth + many tools (>20); MEDIUM = no-auth + read-only; LOW = auth required (likely intentional). As MCP servers proliferate, this becomes the canonical recon for an org's agent extension surface AND a bug-bounty primitive for finding misconfigured public servers.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "mcp_endpoint_finder",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "mcp_endpoint_finder failed");
    return res.output;
  },
});
