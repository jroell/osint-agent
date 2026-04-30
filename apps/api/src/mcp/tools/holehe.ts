import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  email: z.string().email(),
  timeout_seconds: z.number().int().min(5).max(120).default(30),
  only: z.array(z.string()).optional().describe("Optional list of provider module names to limit the scan to (e.g. ['twitter','snapchat']). Default: all ~120 holehe modules."),
});

toolRegistry.register({
  name: "email_holehe",
  description:
    "Check whether an email is registered on ~120 sites by abusing each site's password-reset/signup error responses. NON-NOTIFYING — the target email-owner does NOT receive a reset email. Uses the holehe library. Caveat: detection patterns rot fast; expect both false positives and false negatives. Treat hits as suggestive evidence, not proof.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "email_holehe",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "email_holehe failed");
    return res.output;
  },
});
