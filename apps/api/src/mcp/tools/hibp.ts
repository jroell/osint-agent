import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

// pwned_password_check — FREE k-anonymity API, no key.
const pwnedPwInput = z.object({
  password: z.string().min(1).describe("Plaintext password. NEVER transmitted: only the first 5 chars of its SHA-1 hash are sent to the API."),
});

toolRegistry.register({
  name: "pwned_password_check",
  description:
    "Check if a password has appeared in a public breach using Troy Hunt's Pwned Passwords k-anonymity API. Privacy-preserving: only the first 5 chars of the SHA-1 hash leave this machine. Returns the count of times the password has appeared in known breaches. Free, no API key required.",
  inputSchema: pwnedPwInput,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "pwned_password_check",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pwned_password_check failed");
    return res.output;
  },
});

// hibp_breach_lookup — PAID, requires HIBP_API_KEY ($3.95/mo).
const breachInput = z.object({
  email: z.string().email(),
});

toolRegistry.register({
  name: "hibp_breach_lookup",
  description:
    "Look up which breaches an email address has appeared in via Have I Been Pwned. REQUIRES the HIBP_API_KEY env var on the API server (paid, ~$3.95/mo, https://haveibeenpwned.com/API/Key). Returns each breach's name, date, count of accounts affected, and the data classes exposed (passwords, addresses, etc.).",
  inputSchema: breachInput,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hibp_breach_lookup",
      input: i,
      timeoutMs: 20_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hibp_breach_lookup failed");
    return res.output;
  },
});
