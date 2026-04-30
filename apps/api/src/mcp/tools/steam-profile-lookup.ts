import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  id: z.string().min(2).describe("SteamID64 (17-digit numeric, e.g. '76561197960287930') or vanity URL slug (e.g. 'gabelogannewell'). Full Steam URLs are stripped automatically."),
});

toolRegistry.register({
  name: "steam_profile_lookup",
  description:
    "**Steam community profile resolver** — fetches public Steam profile via free XML API. Auto-detects SteamID64 vs vanity-URL input. Returns: SteamID64 (immutable identifier), display name + custom URL slug, real name (often disclosed!), location/country, member-since date with account age, online state, privacy state, summary/headline, VAC ban + trade ban flags (public regardless of privacy), limited-account flag (alt/throwaway signal), groups list (interest graph), most-played games with hours total + recent. Strong gamer ER: real-name disclosure rate is high, custom_url slug commonly reused on Discord/Twitch/GitHub/Reddit, account-since is verifiable temporal signal, VAC ban = anti-cheat violation history. Free, no auth.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "steam_profile_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "steam_profile_lookup failed");
    return res.output;
  },
});
