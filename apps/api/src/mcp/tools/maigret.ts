import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  username: z.string().min(1),
  top_sites: z.number().int().min(0).max(3000).nullable().default(50)
    .describe("Number of top-ranked sites to scan (default 50 for ~30s scans). Pass null to scan all 3000+ sites (can take 5+ minutes)."),
  timeout_seconds: z.number().int().min(5).max(60).default(25),
});

toolRegistry.register({
  name: "username_search_maigret",
  description:
    "Username enumeration across 3000+ sites via Maigret (Bellingcat-recommended sherlock fork). Returns sites where the username is CLAIMED. Defaults to top-50 sites (~30s); set top_sites=null for full scan. Like all sherlock-family tools, expect both false positives (eager pattern matches) and false negatives (rotted detection rules). Manually verify hits.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "username_search_maigret",
      input: i,
      timeoutMs: 360_000, // up to 6 minutes for full scans
    });
    if (!res.ok) throw new Error(res.error?.message ?? "username_search_maigret failed");
    return res.output;
  },
});
