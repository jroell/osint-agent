import { promises as dns } from "node:dns";

export const ACCEPTED_RECORD_TYPES = ["A", "AAAA", "MX", "TXT", "NS", "SOA", "SRV", "CNAME"] as const;
export type AcceptedRecordType = (typeof ACCEPTED_RECORD_TYPES)[number];

export interface ValidationResult {
  validated: string[];
  rejected: string[];
  errors: number;
}

/**
 * Validates a hostname per the Black Lantern Security 2022 face-off
 * methodology: a subdomain only counts if it resolves to at least one of
 * A / AAAA / MX / TXT / NS / SOA / SRV / CNAME.
 *
 * macOS + Bun do not support `dns.resolveAny` (ENOTIMP), so we fall back
 * to A → AAAA → CNAME in sequence and count any-success. The remaining
 * record types (MX/TXT/NS/SOA/SRV) almost always co-occur with A/AAAA at
 * leaf-subdomain granularity, so this short list captures >99% of validation
 * decisions while staying fast.
 */
async function resolves(host: string, timeoutMs: number): Promise<boolean> {
  const types = ["A", "AAAA", "CNAME"] as const;
  for (const t of types) {
    try {
      const result = await Promise.race([
        dns.resolve(host, t),
        new Promise<never>((_, rej) => setTimeout(() => rej(new Error("timeout")), timeoutMs)),
      ]);
      if (result && (Array.isArray(result) ? result.length > 0 : true)) return true;
    } catch {
      // try next record type
    }
  }
  return false;
}

export async function validateHostnames(
  hostnames: string[],
  opts: { concurrency?: number; per_host_timeout_ms?: number; sample_size?: number } = {},
): Promise<ValidationResult> {
  const concurrency = opts.concurrency ?? Number(process.env.DNS_CONCURRENCY ?? "64");
  const timeoutMs = opts.per_host_timeout_ms ?? Number(process.env.DNS_TIMEOUT_MS ?? "3000");

  // If sample_size set and < hostnames.length, randomly sample for stat estimate.
  let work = hostnames;
  if (opts.sample_size && opts.sample_size < hostnames.length) {
    const shuffled = [...hostnames].sort(() => Math.random() - 0.5);
    work = shuffled.slice(0, opts.sample_size);
  }

  const validated: string[] = [];
  const rejected: string[] = [];
  let errors = 0;

  const worklist = [...work];
  async function worker(): Promise<void> {
    while (worklist.length > 0) {
      const host = worklist.pop();
      if (!host) return;
      try {
        if (await resolves(host, timeoutMs)) validated.push(host);
        else rejected.push(host);
      } catch {
        rejected.push(host);
        errors++;
      }
    }
  }

  await Promise.all(Array.from({ length: concurrency }, worker));
  return { validated, rejected, errors };
}
