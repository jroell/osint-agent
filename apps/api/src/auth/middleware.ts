import { Elysia } from "elysia";
import { verifyIdToken, type FirebaseUser } from "./firebase";
import { sql } from "../db/client";
import { config } from "../config";

export type AuthContext = {
  user: FirebaseUser;
  tenantId: string;
  userId: string;
};

/**
 * Elysia plugin: extracts `Authorization: Bearer <firebase_jwt>`, verifies it,
 * and attaches { user, tenantId, userId } to the request context.
 * Auto-provisions a tenants + users row on first sight of a valid JWT.
 */
export const authPlugin = new Elysia({ name: "auth" })
  .derive({ as: "scoped" }, async ({ request }) => {
    if (config.dev.authBypass) {
      return { auth: devAuthContext() };
    }
    const header = request.headers.get("authorization");
    if (!header?.startsWith("Bearer ")) {
      throw new Response("Missing bearer token", { status: 401 });
    }
    const token = header.slice("Bearer ".length);

    const decoded = await verifyIdToken(token).catch((e) => {
      throw new Response(`Invalid token: ${e.message}`, { status: 401 });
    });

    // Upsert user + tenant (Phase 0 = 1 user : 1 tenant).
    const { userId, tenantId } = await ensureUserAndTenant(decoded);

    return {
      auth: {
        user: decoded,
        tenantId,
        userId,
      } satisfies { user: FirebaseUser; tenantId: string; userId: string },
    };
  });

function devAuthContext(): AuthContext {
  return {
    user: {
      uid: "dev-user",
      email: "dev@localhost",
      name: "Local Dev User",
    } as unknown as FirebaseUser,
    tenantId: "00000000-0000-0000-0000-000000000001",
    userId: "00000000-0000-0000-0000-000000000002",
  };
}

async function ensureUserAndTenant(user: FirebaseUser): Promise<{ userId: string; tenantId: string }> {
  const email = user.email ?? `${user.uid}@unknown.local`;
  const name = user.name ?? email.split("@")[0];

  const result = await sql.begin(async (tx) => {
    // Look up existing user
    const existing = await tx`
      SELECT id, tenant_id FROM users WHERE firebase_uid = ${user.uid} LIMIT 1
    `;
    const existingRow = existing[0];
    if (existingRow) {
      await tx`UPDATE users SET last_seen_at = NOW() WHERE id = ${existingRow.id}`;
      return { userId: existingRow.id as string, tenantId: existingRow.tenant_id as string };
    }

    // Provision tenant (Free tier, 100 credits starting balance)
    const tenantRow = await tx`
      INSERT INTO tenants (name, tier)
      VALUES (${name}, 'free')
      RETURNING id
    `;
    const tenantId = tenantRow[0]!.id as string;

    // Provision user
    const userRow = await tx`
      INSERT INTO users (firebase_uid, tenant_id, email, display_name)
      VALUES (${user.uid}, ${tenantId}, ${email}, ${name})
      RETURNING id
    `;
    const userId = userRow[0]!.id as string;

    return { userId, tenantId };
  });

  return result;
}
