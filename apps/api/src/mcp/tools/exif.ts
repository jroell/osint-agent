import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  url: z.string().url().describe("URL of a publicly-hosted image"),
  timeout_seconds: z.number().int().min(5).max(120).default(20),
});

toolRegistry.register({
  name: "exif_extract_geolocate",
  description:
    "Fetch an image and extract its EXIF tags. When GPS coordinates are present, decodes them to WGS-84 lat/lon and includes a Google Maps link. Most social-media uploads strip EXIF, but reuploads/leaks/cloud-storage-misconfigs frequently retain it.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "exif_extract_geolocate",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "exif_extract_geolocate failed");
    return res.output;
  },
});
