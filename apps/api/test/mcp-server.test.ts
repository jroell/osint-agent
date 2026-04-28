import { describe, it, expect, mock, afterAll } from "bun:test";
import { toolRegistry } from "../src/mcp/tools/registry";
import { sql } from "../src/db/client";

mock.module("../src/auth/firebase", () => ({
  verifyIdToken: async () => ({ uid: "firebase-uid-mcp-test", email: "mcp@test.local", name: "MCP Tester" }),
}));

describe("mcp/tool registry", () => {
  afterAll(async () => {
    await sql`DELETE FROM events WHERE payload->>'tool' = 'hello_tool'`;
    await sql`DELETE FROM credit_ledger WHERE reason LIKE 'tool:hello_tool%'`;
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-mcp-test'`;
    await sql`DELETE FROM tenants WHERE name = 'MCP Tester'`;
  });

  it("invokes hello_tool end-to-end", async () => {
    // Bootstrap tenant + user
    const tRow = await sql`
      INSERT INTO tenants (name, tier, credits_balance)
      VALUES ('MCP Tester', 'free', 100)
      RETURNING id
    `;
    const tenantId = tRow[0]!.id as string;
    const uRow = await sql`
      INSERT INTO users (firebase_uid, tenant_id, email, display_name)
      VALUES ('firebase-uid-mcp-test', ${tenantId}, 'mcp@test.local', 'MCP Tester')
      RETURNING id
    `;
    const userId = uRow[0]!.id as string;

    const result = await toolRegistry.invoke(
      "hello_tool",
      { name: "Jason" },
      { user: { uid: "firebase-uid-mcp-test" } as any, tenantId, userId },
    );

    expect((result as any).greeting).toBe("Hello, Jason!");
    expect((result as any).tenantId).toBe(tenantId);

    // Credit decremented
    const [after] = await sql`SELECT credits_balance FROM tenants WHERE id = ${tenantId}`;
    expect(Number(after!.credits_balance)).toBe(99);

    // Events written
    const events = await sql`
      SELECT event_type FROM events
      WHERE tenant_id = ${tenantId} AND event_type IN ('tool.called', 'tool.succeeded', 'credit.spent')
      ORDER BY created_at ASC
    `;
    expect(events.map((e) => e.event_type)).toEqual(["tool.called", "credit.spent", "tool.succeeded"]);
  });
});
