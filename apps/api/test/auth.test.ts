import { describe, it, expect, beforeAll, afterAll, mock } from "bun:test";
import { Elysia } from "elysia";
import { authPlugin } from "../src/auth/middleware";
import { sql } from "../src/db/client";

// Mock the firebase module so we don't need a live project for unit tests
mock.module("../src/auth/firebase", () => ({
  verifyIdToken: async (token: string) => {
    if (token === "valid-token") {
      return {
        uid: "firebase-uid-test-1",
        email: "test@example.com",
        name: "Test User",
        aud: "osint-agent-dev",
        iss: "https://securetoken.google.com/osint-agent-dev",
      };
    }
    throw new Error("token invalid");
  },
}));

describe("auth middleware", () => {
  beforeAll(async () => {
    // Clean slate for test uid
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-test-1'`;
  });

  afterAll(async () => {
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-test-1'`;
    await sql`DELETE FROM tenants WHERE name = 'test'`;
  });

  const app = new Elysia()
    .use(authPlugin)
    .get("/me", ({ auth }) => ({ uid: auth.user.uid, tenantId: auth.tenantId, userId: auth.userId }));

  it("rejects missing token", async () => {
    const res = await app.handle(new Request("http://localhost/me"));
    expect(res.status).toBe(401);
  });

  it("rejects invalid token", async () => {
    const res = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer bogus" } }),
    );
    expect(res.status).toBe(401);
  });

  it("accepts valid token and auto-provisions user + tenant", async () => {
    const res = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer valid-token" } }),
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.uid).toBe("firebase-uid-test-1");
    expect(body.tenantId).toMatch(/^[0-9a-f-]{36}$/);
    expect(body.userId).toMatch(/^[0-9a-f-]{36}$/);

    // Idempotent: second call reuses same tenant
    const res2 = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer valid-token" } }),
    );
    const body2 = await res2.json();
    expect(body2.tenantId).toBe(body.tenantId);
  });
});
