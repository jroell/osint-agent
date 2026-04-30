import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Apex domain or full URL (e.g. 'vurvey.app' or 'https://api.example.com')"),
  additional_paths: z.array(z.string()).optional().describe("Extra paths to probe in addition to the default ~20"),
});

toolRegistry.register({
  name: "well_known_recon",
  description:
    "**One-shot recon of ~20 standard discovery paths.** Probes /robots.txt, /sitemap.xml, /sitemap_index.xml, /humans.txt, /security.txt + 15 .well-known/ paths in parallel. Auto-parses each by format: openid-configuration → full OAuth/OIDC endpoint surface (issuer, auth/token/userinfo/jwks endpoints, supported scopes/grants), apple-app-site-association → iOS Bundle/Team IDs + universal link paths, assetlinks.json → Android package names + SHA-256 cert fingerprints, security.txt → security disclosure contacts, robots.txt → Disallow paths (the classic intentionally-hidden URL leak), sitemap.xml → all crawler-known URLs (often includes admin/draft/internal pages). Returns highlights for the agent to act on. Use as the FIRST recon step on any new target — single call replaces ~15 manual fetches.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "well_known_recon",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "well_known_recon failed");
    return res.output;
  },
});
