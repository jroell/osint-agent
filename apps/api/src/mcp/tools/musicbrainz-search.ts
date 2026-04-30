import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search_artists", "artist_detail", "search_recordings"])
    .optional()
    .describe(
      "search_artists: name → artists with MBID + ISNI. artist_detail: by MBID → cross-platform URL relations + aliases. search_recordings: title + optional artist → recordings with ISRCs. Auto-detects: mbid → artist_detail, recording → search_recordings, else → search_artists."
    ),
  artist: z
    .string()
    .optional()
    .describe("Artist name (search_artists or search_recordings filter)."),
  query: z.string().optional().describe("Generic query alias for artist."),
  mbid: z.string().optional().describe("MusicBrainz ID for artist_detail (e.g. 'a74b1b7f-71a5-4011-9441-d0b5e4122711' = Radiohead)."),
  recording: z.string().optional().describe("Recording (track) title for search_recordings."),
  limit: z.number().int().min(1).max(25).optional().describe("Max results (default 5 artists / 10 recordings)."),
});

toolRegistry.register({
  name: "musicbrainz_search",
  description:
    "**MusicBrainz — open music metadata DB. Free, no auth (1 req/sec rate limit). 2M+ artists, 30M+ recordings, 5M+ releases.** The 'Wikidata of music' — MBIDs are canonical cross-reference keys for music referenced by Spotify, Apple Music, Discogs, Last.fm, AllMusic, etc. Three modes: (1) **search_artists** — fuzzy name → artists with MBID + country + type (Person/Group/Orchestra) + life-span (begin/end dates) + **ISNI** (International Standard Name Identifier — global authority record key) + IPI codes. Tested with 'Radiohead' → MBID a74b1b7f, formed 1991 UK, ISNI 0000000115475162. (2) **artist_detail** — by MBID → aliases (search hints, transliterations, fan abbreviations like 'r/head' for Radiohead) + URL relations (cross-platform pivots: AllMusic, Bandcamp, Spotify, Apple Music, Discogs, Last.fm, BBC Music, Wikipedia, Songkick, Setlist.fm, official social media — typically 15-30 platform IDs per artist). (3) **search_recordings** — title + artist → recordings with **ISRCs** (International Standard Recording Code) + release placements. Closes media-ER triad with `openlibrary_search` (books) and `wikidata_lookup` (movies/general).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "musicbrainz_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "musicbrainz_search failed");
    return res.output;
  },
});
