import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Search query — brand name, internal hostname stem, or specific image name"),
  limit: z.number().int().min(1).max(100).default(25).describe("Max results"),
});

toolRegistry.register({
  name: "docker_hub_search",
  description:
    "**The third leg of the leak-discovery triad** alongside `github_code_search` (code) and `postman_public_search` (APIs). Searches Docker Hub's public v2 API for images by query string. Auto-classifies leak severity by name patterns: critical (-internal/-secret/-private/-prod), high (-dev/-staging/-ci/-debug), medium (-config/-tools/-deploy), low (everything else). Critical/high results often contain hardcoded credentials in ENV layers, internal CA certs, or references to private container registries that map an org's infrastructure. Pair with `docker pull <image>` + layer inspection (out of MCP scope but URL is returned).",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "docker_hub_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "docker_hub_search failed");
    return res.output;
  },
});
