import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Apex domain or specific URL (e.g. 'vurvey.app')"),
  categories: z.array(z.enum([
    "exposed_configs", "credentials_in_files", "admin_panels", "file_indexes",
    "error_messages", "backup_files", "exposed_apis", "log_files",
    "file_extension_dorks", "github_leaks", "subdomain_enum", "cloud_storage",
  ])).optional().describe("Filter to specific categories. Default: include all."),
});

toolRegistry.register({
  name: "google_dork_helper",
  description:
    "**Recon-menu generator** — produces ~25 categorized Google dork query templates targeting a domain. Each dork includes severity (critical = config/credentials; high = admin panels/SQL errors; medium = APIs/docs; low = subdomain enum) + ready-to-click Google/Bing/DDG URLs. Pure utility — no API. Categories: exposed_configs, credentials_in_files, admin_panels, file_indexes, error_messages, backup_files, exposed_apis, log_files, file_extension_dorks, github_leaks, subdomain_enum, cloud_storage. Pair with `google_dork_search` to programmatically execute. Use on authorized targets only.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "google_dork_helper", input: i, timeoutMs: 5_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_dork_helper failed");
    return res.output;
  },
});
