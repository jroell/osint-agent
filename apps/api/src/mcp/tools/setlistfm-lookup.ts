import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search_setlists", "setlist_by_id", "search_artists"]).optional(),
  artist_name: z.string().optional(),
  year: z.string().optional(),
  city_name: z.string().optional(),
  venue_name: z.string().optional(),
  setlist_id: z.string().optional(),
  search_artists: z.boolean().optional().describe("Set true with artist_name to search for artists rather than setlists."),
});

toolRegistry.register({
  name: "setlistfm_lookup",
  description:
    "**Setlist.fm — concert-by-concert setlists. REQUIRES SETLISTFM_API_KEY.** Modes: search_setlists (by artist+year+city+venue), setlist_by_id, search_artists. Each output emits typed entity envelope (kind: concert | artist) with stable Setlist.fm URLs and full song lists for ER chaining.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "setlistfm_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "setlistfm_lookup failed");
    return res.output;
  },
});
