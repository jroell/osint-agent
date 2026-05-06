import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum([
      "search_shows",
      "show_details",
      "episodes_list",
      "episode_by_number",
      "search_people",
      "person_details",
    ])
    .optional()
    .describe(
      "search_shows: keyword. show_details: tvmaze id. episodes_list: all episodes for show id. episode_by_number: show_id+season+number → specific episode. search_people: person keyword. person_details: tvmaze person id."
    ),
  query: z.string().optional().describe("Keyword for search_shows / search_people."),
  who: z.enum(["show", "person"]).optional().describe("Disambiguates 'query' between shows and people; defaults to show."),
  show_id: z.number().int().optional().describe("TVMaze show id."),
  person_id: z.number().int().optional().describe("TVMaze person id."),
  season: z.number().int().optional().describe("Season number for episode_by_number."),
  number: z.number().int().optional().describe("Episode number for episode_by_number."),
});

toolRegistry.register({
  name: "tvmaze_lookup",
  description:
    "**TVMaze — free no-key TV-show metadata.** Complementary coverage to TMDB (stronger on US cable). Modes: search_shows, show_details, episodes_list, episode_by_number, search_people, person_details. Each record emits a typed entity envelope (kind: tv_show | tv_episode | person) with TVMaze+IMDb cross-references — suitable for ER chaining. Use as fallback when TMDB_API_KEY unavailable, or alongside TMDB to triangulate episode-level facts.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tvmaze_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tvmaze_lookup failed");
    return res.output;
  },
});
