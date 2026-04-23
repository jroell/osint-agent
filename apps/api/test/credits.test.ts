import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { spendCredits, grantCredits, InsufficientCreditsError } from "../src/billing/credits";
import { sql, closeDb } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000002";

describe("billing/credits", () => {
  beforeAll(async () => {
    await sql`
      INSERT INTO tenants (id, name, tier, credits_balance)
      VALUES (${TENANT_ID}, 'credits-test', 'free', 1000)
      ON CONFLICT (id) DO UPDATE SET credits_balance = 1000
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM credit_ledger WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
    await closeDb();
  });

  it("spends credits and writes ledger + event", async () => {
    const { newBalance } = await spendCredits({
      tenantId: TENANT_ID,
      millicredits: 200,
      reason: "tool:dns_lookup",
    });
    expect(newBalance).toBe(800);

    const [row] = await sql`
      SELECT delta_millicredits, reason FROM credit_ledger
      WHERE tenant_id = ${TENANT_ID}
      ORDER BY created_at DESC LIMIT 1
    `;
    expect(Number(row.delta_millicredits)).toBe(-200);
    expect(row.reason).toBe("tool:dns_lookup");
  });

  it("refuses to spend past zero", async () => {
    await expect(
      spendCredits({ tenantId: TENANT_ID, millicredits: 99999, reason: "tool:expensive" }),
    ).rejects.toThrow(InsufficientCreditsError);
  });

  it("grants credits", async () => {
    const { newBalance } = await grantCredits({
      tenantId: TENANT_ID,
      millicredits: 500,
      reason: "refill:hunter",
    });
    expect(newBalance).toBeGreaterThanOrEqual(1300);
  });
});
