import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  email: z.string().email(),
  timeout_seconds: z.number().int().min(10).max(180).default(60),
});

toolRegistry.register({
  name: "google_account_ghunt",
  description:
    "Enrich a Google account by email — returns full name, profile photo, public Maps reviews, YouTube uploads, Photos albums, Calendar info, and Google ID. REQUIRES one-time GHunt authentication on the API server: run `ghunt login` once to provision ~/.malfrats/ghunt/creds.m (you'll paste a master_token + oauth_token from a logged-in Google session). Without creds the tool returns a clear error with setup instructions.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "google_account_ghunt",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_account_ghunt failed");
    return res.output;
  },
});
