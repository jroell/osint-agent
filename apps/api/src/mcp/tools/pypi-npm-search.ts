import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Search query — brand name or org-name"),
  limit: z.number().int().min(1).max(100).default(20).describe("Max results per registry"),
  skip_pypi: z.boolean().default(false),
  skip_npm: z.boolean().default(false),
});

toolRegistry.register({
  name: "pypi_npm_search",
  description:
    "**Fifth leg of the leak-discovery stack** — searches PyPI and npm public registries in parallel. Auto-classifies each package by name pattern: critical (-internal/-secret/-private/-prod), high (-dev/-staging/-test/-debug), low (everything else). Reveals: (1) an org's open-source posture, (2) accidentally-public internal packages, (3) dependency-confusion attack surface — internal package names not yet claimed on public registries. Returns: package name, version, author, maintainers, license, last upload date, package URL. Free, no key. Pairs with `github_code_search` (code) + `postman_public_search` (APIs) + `docker_hub_search` (containers) for a 5-channel leak-discovery stack.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "pypi_npm_search",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pypi_npm_search failed");
    return res.output;
  },
});
