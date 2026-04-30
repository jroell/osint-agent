import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Email address, name, or PGP fingerprint/key ID"),
});

toolRegistry.register({
  name: "pgp_key_lookup",
  description:
    "**Identity-correlation primitive via PGP keyservers.** Queries 3 public HKP keyservers (keys.openpgp.org, keyserver.ubuntu.com, pgp.mit.edu) in parallel for keys matching an email/name/fingerprint. The killer ER signal: a single PGP key often has MULTIPLE UIDs — a person's personal email + work email + alternate name = all on one key = same identity. Returns: fingerprints across servers (cross-validation), all UIDs (User IDs), aggregated unique emails+names, key creation/expiry dates. Free, no key. Privacy-respecting: keyservers don't log lookups.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "pgp_key_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pgp_key_lookup failed");
    return res.output;
  },
});
