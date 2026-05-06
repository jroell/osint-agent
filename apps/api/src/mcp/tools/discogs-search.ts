import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "release", "master_release", "artist"]).optional(),
  query: z.string().optional(),
  type: z.enum(["release", "master", "artist", "label"]).optional(),
  release_id: z.string().optional(),
  master_id: z.string().optional(),
  artist_id: z.string().optional(),
});

toolRegistry.register({
  name: "discogs_search",
  description:
    "**Discogs (api.discogs.com) — music release / tracklist database. Free, optional DISCOGS_TOKEN for higher rate limits.** Discogs has tracklist-level metadata and rare/regional releases that MusicBrainz misses. Modes: search (across releases/masters/artists/labels), release (with full tracklist), master_release, artist. Each output emits typed entity envelope (kind: release | master_release | artist | label) with stable Discogs IDs.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "discogs_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "discogs_search failed");
    return res.output;
  },
});
