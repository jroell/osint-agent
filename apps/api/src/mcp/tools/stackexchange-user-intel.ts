import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  user_id: z.number().int().positive().describe("Stack Exchange user ID on the primary site (e.g. 22656 = Jon Skeet on Stack Overflow)"),
  site: z.string().default("stackoverflow").describe("Primary SE site to query — 'stackoverflow' (default), 'serverfault', 'security', 'math', 'tex', 'unix', etc."),
});

toolRegistry.register({
  name: "stackexchange_user_intel",
  description:
    "**Stack Exchange network deep dive** — per-user intel across the entire 170+ site SE network. Free public API. Returns: profile (display name, account_id, reputation, location, website, about_me, account age, badges bronze/silver/gold), cross-site activity (every SE site the user is active on, sorted by reputation — reveals NICHE personal interests like Mi Yodeya/Bicycles/Politics that are invisible to SO-only analytics), top tags (expertise depth — 19,981 answers tagged C# = THE C# expert), total network reputation. Highlights flag non-tech SE sites separately (interest-disclosure ER signal). Use cases: tech-identity ER, expertise profiling, conflict-of-interest detection (someone heavily active on a topic-specific SE), cross-site account confirmation. Free, no auth (10K req/day).",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "stackexchange_user_intel", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "stackexchange_user_intel failed");
    return res.output;
  },
});
