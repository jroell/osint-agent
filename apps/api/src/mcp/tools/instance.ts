import { z } from "zod";
import type { AuthContext } from "../../auth/middleware";
import { spendCredits, InsufficientCreditsError } from "../../billing/credits";
import { writeEvent } from "../../events/stream";

export interface ToolDefinition<Input extends z.ZodType> {
  name: string;
  description: string;
  inputSchema: Input;
  /** Cost in millicredits — deducted BEFORE execution; refunded on failure. */
  costMillicredits: number;
  handler: (input: z.infer<Input>, ctx: AuthContext) => Promise<unknown>;
}

export class ToolRegistry {
  private tools = new Map<string, ToolDefinition<z.ZodType>>();

  register<I extends z.ZodType>(def: ToolDefinition<I>): void {
    if (this.tools.has(def.name)) throw new Error(`Duplicate tool: ${def.name}`);
    this.tools.set(def.name, def);
  }

  list(): Array<{ name: string; description: string; inputSchema: z.ZodType }> {
    return Array.from(this.tools.values()).map((t) => ({
      name: t.name,
      description: t.description,
      inputSchema: t.inputSchema,
    }));
  }

  async invoke(name: string, input: unknown, ctx: AuthContext): Promise<unknown> {
    const tool = this.tools.get(name);
    if (!tool) throw new Error(`Unknown tool: ${name}`);

    const parsed = tool.inputSchema.parse(input);
    const traceId = crypto.randomUUID();

    await writeEvent({
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      eventType: "tool.called",
      payload: { tool: name, input: parsed },
      traceId,
    });

    try {
      await spendCredits({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        millicredits: tool.costMillicredits,
        reason: `tool:${name}`,
        traceId,
      });
    } catch (e) {
      if (e instanceof InsufficientCreditsError) {
        await writeEvent({
          tenantId: ctx.tenantId,
          userId: ctx.userId,
          eventType: "tool.failed",
          payload: { tool: name, reason: "insufficient_credits" },
          traceId,
        });
        throw e;
      }
      throw e;
    }

    try {
      const result = await tool.handler(parsed, ctx);
      await writeEvent({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        eventType: "tool.succeeded",
        payload: { tool: name },
        traceId,
      });
      return result;
    } catch (e) {
      // Refund on failure
      await spendCredits({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        millicredits: -tool.costMillicredits,
        reason: `refund:${name}`,
        traceId,
      }).catch(() => {}); // refund is best-effort; don't mask the original error
      await writeEvent({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        eventType: "tool.failed",
        payload: { tool: name, error: (e as Error).message },
        traceId,
      });
      throw e;
    }
  }
}

export const toolRegistry = new ToolRegistry();
