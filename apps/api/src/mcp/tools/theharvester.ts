import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  domain: z.string().min(3),
  sources: z.string().optional().describe("Comma-separated list of theHarvester sources. Default uses free sources only (anubis, crtsh, dnsdumpster, hackertarget, otx, rapiddns, threatcrowd, urlscan). Override to use commercial sources after configuring keys in ~/.theHarvester/api-keys.yaml."),
  limit: z.number().int().min(10).max(1000).default(100),
  timeout_seconds: z.number().int().min(15).max(300).default(90),
});

toolRegistry.register({
  name: "theharvester",
  description:
    "Multi-source domain reconnaissance via theHarvester: enumerates subdomains, IPs, emails, ASNs, and surfaced URLs across crt.sh, DNSDumpster, AlienVault OTX, HackerTarget, ThreatCrowd, RapidDNS, urlscan, and Anubis (free sources by default). Add commercial sources after configuring keys.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "theharvester",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "theharvester failed");
    return res.output;
  },
});
