import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum([
      "search_movie",
      "search_tv",
      "search_person",
      "search_multi",
      "movie_details",
      "tv_details",
      "tv_season_details",
      "tv_episode_details",
      "person_details",
      "movie_credits",
      "tv_credits",
      "person_credits",
    ])
    .optional()
    .describe(
      "search_*: keyword search (multi covers all 3 types). *_details: full record by TMDB id. tv_episode_details requires tv_id+season_number+episode_number. *_credits returns cast+crew. Auto-detects from inputs."
    ),
  query: z.string().optional().describe("Keyword for any search_* mode."),
  year: z.number().int().optional().describe("Optional year filter for search_movie / search_tv."),
  movie_id: z.number().int().optional().describe("TMDB movie id."),
  tv_id: z.number().int().optional().describe("TMDB TV-show id."),
  person_id: z.number().int().optional().describe("TMDB person id."),
  season_number: z.number().int().optional().describe("Season number for tv_season_details / tv_episode_details."),
  episode_number: z.number().int().optional().describe("Episode number within season for tv_episode_details."),
});

toolRegistry.register({
  name: "tmdb_lookup",
  description:
    "**TMDB (The Movie Database) — canonical episode-level film/TV metadata.** REQUIRES TMDB_API_KEY. 12 modes covering search (movie/tv/person/multi), full details (movie/tv/person), full TV season episode lists, individual episode credits with director+writer+guest stars, and full credit rolls. Each call emits typed entities (kind: movie | tv_show | tv_episode | person) with TMDB+IMDb stable IDs, suitable for direct ingest by `panel_entity_resolution` and `entity_link_finder`. Pairs with `tvmaze_lookup` (free no-key fallback with different coverage), `wikipedia_search` (article-level cross-reference), and the regional-cinema / dominant-practitioner heuristics in agent memory.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tmdb_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tmdb_lookup failed");
    return res.output;
  },
});
