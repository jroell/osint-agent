import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(1).describe("X/Twitter handle, with or without leading @"),
});

toolRegistry.register({
  name: "twitter_user",
  description:
    "Fetch an X/Twitter user profile via the official X API v2. Returns name, bio, location, verification status, public metrics (followers/following/tweet count), creation date. REQUIRES X_API_BEARER_TOKEN env var (paid Premium tier $100+/mo, https://developer.x.com/en/portal/products). There is no reliable free path: snscrape is dead, Nitter is mostly broken, and X aggressively blocks scrapers.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "twitter_user",
      input: i,
      timeoutMs: 20_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "twitter_user failed");
    return res.output;
  },
});
