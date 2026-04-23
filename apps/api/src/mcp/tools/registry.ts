import { z } from "zod";
import { toolRegistry } from "./instance";

export { ToolRegistry, toolRegistry } from "./instance";
export type { ToolDefinition } from "./instance";

toolRegistry.register({
  name: "hello_tool",
  description: "Sanity-check tool. Returns a greeting and the authenticated tenant ID.",
  inputSchema: z.object({ name: z.string().default("world") }),
  costMillicredits: 1,
  handler: async (input, ctx) => ({
    greeting: `Hello, ${input.name}!`,
    tenantId: ctx.tenantId,
    now: new Date().toISOString(),
  }),
});

// Side-effect imports: registering tools.
// Keep this block at the bottom of registry.ts; order matters for the registry.
import "./stealth-http";
import "./subfinder";
import "./dns-lookup";
