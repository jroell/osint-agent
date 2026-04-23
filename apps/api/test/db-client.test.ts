import { describe, it, expect, afterAll } from "bun:test";
import { sql, closeDb } from "../src/db/client";

describe("db/client", () => {
  it("connects and executes SELECT 1", async () => {
    const rows = await sql`SELECT 1 AS one`;
    expect(rows[0].one).toBe(1);
  });

  it("sees the tenants table", async () => {
    const rows = await sql`
      SELECT to_regclass('public.tenants') AS t
    `;
    expect(rows[0].t).toBe("tenants");
  });

  afterAll(async () => {
    await closeDb();
  });
});
