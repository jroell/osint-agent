import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { toolRegistry } from "./tools/registry";
import type { AuthContext } from "../auth/middleware";
import { logger } from "../telemetry";

export function buildMcpServer(ctx: AuthContext): McpServer {
  const server = new McpServer({
    name: "osint-agent",
    version: "0.1.0",
  });

  for (const tool of toolRegistry.list()) {
    server.tool(
      tool.name,
      tool.description,
      // Note: MCP SDK accepts raw Zod schemas; runtime parse happens in ToolRegistry
      tool.inputSchema as unknown as Record<string, unknown>,
      async (input) => {
        try {
          const result = await toolRegistry.invoke(tool.name, input, ctx);
          return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        } catch (e) {
          logger.error({ err: e, tool: tool.name }, "tool invocation failed");
          return { content: [{ type: "text", text: `Error: ${(e as Error).message}` }], isError: true };
        }
      },
    );
  }

  return server;
}

/**
 * Returns a ready-to-mount Streamable HTTP transport for an authenticated context.
 * One transport per session; reuse via a session table (Phase 1) — here, we build fresh.
 */
export function streamableTransport(): StreamableHTTPServerTransport {
  return new StreamableHTTPServerTransport({
    sessionIdGenerator: () => crypto.randomUUID(),
  });
}
