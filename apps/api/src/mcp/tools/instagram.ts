import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(1).describe("Instagram username, with or without leading @"),
});

toolRegistry.register({
  name: "instagram_user",
  description:
    "Fetch an Instagram public profile via Apify's instagram-profile-scraper actor. Returns full name, bio, follower/following/posts counts, business contact (email/phone) if set. REQUIRES APIFY_API_TOKEN env var ($5+/mo, https://apify.com/apify/instagram-profile-scraper). Instagram blocks all unauthenticated scrapers — paid Apify is the most reliable path.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "instagram_user",
      input: i,
      timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "instagram_user failed");
    return res.output;
  },
});
