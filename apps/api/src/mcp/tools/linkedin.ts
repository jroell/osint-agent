import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().describe("Full LinkedIn profile URL (e.g. https://www.linkedin.com/in/williamhgates/)"),
});

toolRegistry.register({
  name: "linkedin_proxycurl",
  description:
    "Fetch a LinkedIn profile via Proxycurl (the cleanest paid path; LinkedIn aggressively blocks all scrapers). Returns full name, headline, location, summary, experiences, education, skills, languages, and connection/follower counts. REQUIRES PROXYCURL_API_KEY env var ($49+/mo, https://nubela.co/proxycurl/pricing).",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "linkedin_proxycurl",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "linkedin_proxycurl failed");
    return res.output;
  },
});
