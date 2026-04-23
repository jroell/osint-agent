import { describe, it, expect, mock, beforeAll, afterAll } from "bun:test";
import { sql } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000099";

mock.module("stripe", () => {
  class FakeStripe {
    webhooks = {
      constructEvent: (body: string) => JSON.parse(body),
    };
    subscriptions = {
      retrieve: async (_id: string) => ({
        items: { data: [{ price: { id: process.env.STRIPE_PRICE_ID_HUNTER } }] },
      }),
    };
  }
  return { default: FakeStripe };
});

const { handleStripeWebhook } = await import("../src/billing/webhook");

describe("stripe webhook", () => {
  beforeAll(async () => {
    await sql`
      INSERT INTO tenants (id, name, tier, credits_balance)
      VALUES (${TENANT_ID}, 'stripe-test', 'free', 0)
      ON CONFLICT (id) DO UPDATE SET tier = 'free', credits_balance = 0
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM credit_ledger WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
  });

  it("checkout.session.completed upgrades tier and grants credits", async () => {
    const event = {
      type: "checkout.session.completed",
      data: {
        object: {
          client_reference_id: TENANT_ID,
          customer: "cus_test",
          subscription: "sub_test",
          id: "cs_test",
        },
      },
    };
    await handleStripeWebhook(JSON.stringify(event), "sig");

    const [row] = await sql`SELECT tier, credits_balance FROM tenants WHERE id = ${TENANT_ID}`;
    expect(row!.tier).toBe("hunter");
    expect(Number(row!.credits_balance)).toBe(500_000);
  });
});
