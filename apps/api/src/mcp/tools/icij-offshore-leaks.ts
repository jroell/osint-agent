import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "entity", "node_relationships"]).optional(),
  query: z.string().optional().describe("Free-text search."),
  entity_type: z.enum(["entity", "company", "officer", "person", "intermediary", "address"]).optional(),
  node_id: z.string().optional().describe("ICIJ node id."),
  node_type: z.string().optional().describe("Type hint for node fetch."),
  relationships_for: z.string().optional().describe("Node id whose 1-hop relationships to fetch."),
});

toolRegistry.register({
  name: "icij_offshore_leaks",
  description:
    "**ICIJ Offshore Leaks Database (offshoreleaks.icij.org) — free no-key, ~810k+ entities/officers/intermediaries from Pandora/Paradise/Panama/Bahamas/Offshore/Swissleaks investigations.** One of the highest-value beneficial-ownership / shell-company OSINT sources. Modes: search (full-text with type filter), entity (node by id), node_relationships (1-hop edges). Each output emits typed entity envelope (kind: person | organization | intermediary | address | relationship) with stable ICIJ URLs. Pairs with `opencorporates`, `opensanctions`, `gleif_lei_lookup` for cross-reference.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "icij_offshore_leaks",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "icij_offshore_leaks failed");
    return res.output;
  },
});
