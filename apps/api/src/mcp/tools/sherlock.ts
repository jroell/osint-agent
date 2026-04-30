import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  username: z.string().min(1),
  timeout_seconds: z.number().int().min(5).max(180).default(60),
});

toolRegistry.register({
  name: "username_search_sherlock",
  description:
    "Username enumeration across ~400 sites via the original Sherlock. Smaller catalog than Maigret but the most widely-recognized tool in the OSINT community. Same caveats as all sherlock-family tools: expect false positives and negatives; manually verify hits.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "username_search_sherlock",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "username_search_sherlock failed");
    return res.output;
  },
});
