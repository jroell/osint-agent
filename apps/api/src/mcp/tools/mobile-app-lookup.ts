import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  ios_bundle_id: z.string().optional().describe("Single iOS Bundle ID (e.g. 'com.openai.chat'). Strips Apple Team ID prefix automatically."),
  ios_bundle_ids: z.array(z.string()).optional().describe("Multiple iOS Bundle IDs — batched in one iTunes API call"),
  android_package_name: z.string().optional().describe("Single Android package name (e.g. 'com.github.android')"),
  android_package_names: z.array(z.string()).optional().describe("Multiple Android package names — looked up in parallel"),
}).refine(d => d.ios_bundle_id || (d.ios_bundle_ids?.length ?? 0) > 0 || d.android_package_name || (d.android_package_names?.length ?? 0) > 0, {
  message: "At least one iOS or Android identifier required",
});

toolRegistry.register({
  name: "mobile_app_lookup",
  description:
    "**Resolves iOS Bundle IDs and Android package names to full app metadata.** Designed as the natural follow-up to `well_known_recon` (which surfaces IDs from /.well-known/apple-app-site-association and /.well-known/assetlinks.json). For iOS: uses Apple's free iTunes Lookup API (no key, batched call) — returns app name, sellerName (legal entity, often reveals shell-cos), version, release notes, screenshots, ratings, content rating, supported devices, languages, primary genre, file size. For Android: scrapes Google Play HTML — returns app name, developer, current version, install band, last updated, developer email/website, genre. Strips Apple Team ID prefixes automatically (e.g. 'XN94N9AMD4.com.openai.chat' → 'com.openai.chat').",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "mobile_app_lookup",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "mobile_app_lookup failed");
    return res.output;
  },
});
