import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { writeEvent } from "../src/events/stream";
import { sql } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000001";

describe("events/stream", () => {
  beforeAll(async () => {
    // Create a test tenant
    await sql`
      INSERT INTO tenants (id, name, tier)
      VALUES (${TENANT_ID}, 'event-test-tenant', 'free')
      ON CONFLICT (id) DO NOTHING
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
  });

  it("writes an event and can read it back", async () => {
    await writeEvent({
      tenantId: TENANT_ID,
      eventType: "tool.called",
      payload: { tool: "dns_lookup", target: "example.com" },
    });

    const rows = await sql`
      SELECT event_type, payload
      FROM events
      WHERE tenant_id = ${TENANT_ID}
      ORDER BY created_at DESC
      LIMIT 1
    `;
    expect(rows[0]!.event_type).toBe("tool.called");
    expect(rows[0]!.payload.tool).toBe("dns_lookup");
  });
});
