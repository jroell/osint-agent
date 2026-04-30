import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
  limit: z.number().int().min(1).max(100).default(25),
  department: z.string().optional().describe("Filter by department (executive, finance, hr, it, marketing, etc.)"),
  seniority: z.string().optional().describe("Filter by seniority (junior, senior, executive)"),
});

toolRegistry.register({
  name: "hunter_io_email_finder",
  description:
    "Find public emails associated with a domain via Hunter.io's database. Returns email + name + position + LinkedIn/Twitter handles + sources where the email was scraped from. REQUIRES HUNTER_IO_API_KEY env var (free tier: 25 searches/month, https://hunter.io/users/sign_up). Best email-by-domain finder publicly available.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hunter_io_email_finder",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hunter_io_email_finder failed");
    return res.output;
  },
});
