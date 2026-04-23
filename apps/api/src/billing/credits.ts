import { sql } from "../db/client";
import { writeEvent } from "../events/stream";

export class InsufficientCreditsError extends Error {
  constructor(public readonly needed: number, public readonly available: number) {
    super(`Insufficient credits: need ${needed} millicredits, have ${available}`);
  }
}

/**
 * Atomically spends credits. Throws InsufficientCreditsError on underflow.
 * Writes to credit_ledger + updates tenants.credits_balance in one transaction.
 */
export async function spendCredits(args: {
  tenantId: string;
  userId?: string;
  millicredits: number;
  reason: string;
  metadata?: Record<string, unknown>;
  traceId?: string;
}): Promise<{ newBalance: number }> {
  if (args.millicredits <= 0) throw new Error("millicredits must be positive for spend");

  return await sql.begin(async (tx) => {
    const rows = await tx<{ credits_balance: string }[]>`
      UPDATE tenants
      SET credits_balance = credits_balance - ${args.millicredits}
      WHERE id = ${args.tenantId} AND credits_balance >= ${args.millicredits}
      RETURNING credits_balance
    `;
    if (rows.length === 0) {
      const avail = await tx<{ credits_balance: string }[]>`
        SELECT credits_balance FROM tenants WHERE id = ${args.tenantId}
      `;
      throw new InsufficientCreditsError(args.millicredits, Number(avail[0]?.credits_balance ?? 0));
    }

    await tx`
      INSERT INTO credit_ledger (tenant_id, user_id, delta_millicredits, reason, metadata)
      VALUES (${args.tenantId}, ${args.userId ?? null}, ${-args.millicredits}, ${args.reason}, ${JSON.stringify(args.metadata ?? {})}::jsonb)
    `;

    // Fire-and-wait event write within the same tx so we never lose a billing event
    await writeEvent({
      tenantId: args.tenantId,
      userId: args.userId ?? null,
      eventType: "credit.spent",
      payload: { millicredits: args.millicredits, reason: args.reason, ...(args.metadata ?? {}) },
      traceId: args.traceId,
    });

    return { newBalance: Number(rows[0].credits_balance) };
  });
}

export async function grantCredits(args: {
  tenantId: string;
  userId?: string;
  millicredits: number;
  reason: string;
  metadata?: Record<string, unknown>;
}): Promise<{ newBalance: number }> {
  if (args.millicredits <= 0) throw new Error("millicredits must be positive for grant");

  return await sql.begin(async (tx) => {
    const rows = await tx<{ credits_balance: string }[]>`
      UPDATE tenants
      SET credits_balance = credits_balance + ${args.millicredits}
      WHERE id = ${args.tenantId}
      RETURNING credits_balance
    `;
    await tx`
      INSERT INTO credit_ledger (tenant_id, user_id, delta_millicredits, reason, metadata)
      VALUES (${args.tenantId}, ${args.userId ?? null}, ${args.millicredits}, ${args.reason}, ${JSON.stringify(args.metadata ?? {})}::jsonb)
    `;
    await writeEvent({
      tenantId: args.tenantId,
      userId: args.userId ?? null,
      eventType: "credit.granted",
      payload: { millicredits: args.millicredits, reason: args.reason, ...(args.metadata ?? {}) },
    });
    return { newBalance: Number(rows[0].credits_balance) };
  });
}
