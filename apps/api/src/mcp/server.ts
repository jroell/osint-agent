import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js";
import { toolRegistry } from "./tools/registry";
import { registerPrompts } from "./prompts";
import type { AuthContext } from "../auth/middleware";
import { logger } from "../telemetry";

export function buildMcpServer(ctx: AuthContext): McpServer {
  const server = new McpServer({
    name: "osint-agent",
    version: "0.1.0",
  });

  registerPrompts(server);

  for (const tool of toolRegistry.list()) {
    server.registerTool(
      tool.name,
      {
        description: tool.description,
        inputSchema: tool.inputSchema,
      },
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
 * Phase-0 stateless transport: each HTTP request is an independent JSON-RPC
 * exchange with no cross-request session state. Session-stateful mode (with
 * sessionIdGenerator + Mcp-Session-Id reuse) lands in Phase 1.
 */
export function streamableTransport(): WebStandardStreamableHTTPServerTransport {
  return new WebStandardStreamableHTTPServerTransport({
    sessionIdGenerator: undefined,
    enableJsonResponse: true,
  });
}
