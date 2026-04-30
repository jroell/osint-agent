import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  entity_a: z.string().min(2).describe("First entity name (person or organization)"),
  entity_b: z.string().min(2).describe("Second entity name"),
  type_a: z.enum(["Person", "Organization"]).optional(),
  type_b: z.enum(["Person", "Organization"]).optional(),
});

toolRegistry.register({
  name: "entity_link_finder",
  description:
    "**Marquee 'connecting the dots' tool.** Given two entities (people or organizations), traces 1-hop connection paths through Diffbot's Knowledge Graph: shared employers, shared schools, shared board memberships, co-founded organizations, and directional founder/employee relationships. Returns each connection with the bridging entity (e.g., 'Anthropic'), both parties' roles + time periods, and a confidence score based on temporal overlap. The literal 'are X and Y connected, and how?' primitive — Diffbot KG's curated 200B-fact graph powers high-precision answers when both entities are indexed. REQUIRES DIFFBOT_API_KEY.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "entity_link_finder",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "entity_link_finder failed");
    return res.output;
  },
});
