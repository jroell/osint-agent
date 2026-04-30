import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  login: z.string().min(1).describe("GitHub user or org login (e.g. 'anthropics')"),
});

toolRegistry.register({
  name: "github_user_profile",
  description:
    "Fetch a GitHub user or organization's public profile, top repos by stars, and recently-pushed repos. Works without auth (60 req/hr); set GITHUB_TOKEN for 5000 req/hr. Useful for attributing personas, finding adjacent repos, and surfacing public activity timelines.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "github_user_profile",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "github_user_profile failed");
    return res.output;
  },
});
