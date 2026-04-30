import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["lookup_ip", "search", "country", "top_by_weight"])
    .optional()
    .describe(
      "lookup_ip: IP → matching relay or 'not in Tor'. search: nickname/contact/AS substring. country: relays in country code with optional flag filter. top_by_weight: highest-consensus-weight relays globally. Auto-detects: ip → lookup_ip, country → country, query → search, else → top_by_weight."
    ),
  ip: z.string().optional().describe("IP address to check (lookup_ip mode)."),
  query: z.string().optional().describe("Substring across nickname/contact/AS/fingerprint (search mode)."),
  country: z.string().optional().describe("2-letter ISO country code (country mode), e.g. 'us', 'de', 'ru', 'cn'."),
  flag: z
    .enum(["exit", "guard", "fast", "stable", "badexit", "hsdir", "v2dir"])
    .optional()
    .describe("Filter by Tor flag (country / top_by_weight modes)."),
  running_only: z.boolean().optional().describe("Limit to currently-running relays (default true)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max results."),
});

toolRegistry.register({
  name: "tor_relay_lookup",
  description:
    "**Tor Project Onionoo — definitive answer to 'is this IP a Tor relay right now?' plus full operator attribution. Free, no auth.** ~7,000 running relays + ~2,000 bridges, hourly-updated. Four modes: (1) **lookup_ip** — by IP → matching relay (or 'not in Tor') with country, AS, bandwidth, flags (Exit/Guard/BadExit/Stable/Fast/HSDir), and **operator contact field PARSED into structured attribution: email, abuse contact, URL, KeyBase ID, Twitter, Bitcoin donation address, hoster** — relay operators routinely publish this for transparency. (2) **search** — fuzzy by nickname/contact/AS/fingerprint. (3) **country** — relays in country with optional flag filter (e.g. all exits in US). (4) **top_by_weight** — highest-consensus-weight relays (the 'who controls Tor traffic' ranking). **Why this matters**: ip_intel_lookup gives general proxy/hosting flags; this gives Tor-specific status + the operator's actual identity claims. The **BadExit** flag is uniquely high-signal: relays Tor authorities have observed misbehaving. Tested with QuintexAirVPN1 → exit relay in US, 1 Gbps, parsed contact yielded email john@quintex.com + twitter aquintex + keybase aquintex + BTC donation address.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tor_relay_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tor_relay_lookup failed");
    return res.output;
  },
});
