import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(2).describe("Brand or domain (e.g. 'anthropic' or 'anthropic.com'). The tool generates name permutations and probes S3, GCS, and Azure Blob."),
  concurrency: z.number().int().min(1).max(64).default(16),
});

toolRegistry.register({
  name: "exposed_asset_find",
  description:
    "Enumerate probable cloud-storage bucket names across S3, Google Cloud Storage, and Azure Blob — common variants like <target>, <target>-prod, -staging, -backup, -assets, -uploads, etc. Returns existing buckets with their permission state (public-read / public-list / private). Read-only — never writes. Useful for surface-area mapping in bug bounty.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "exposed_asset_find",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "exposed_asset_find failed");
    return res.output;
  },
});
