import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  jwt: z.string().optional().describe("Single JWT string (with or without 'Bearer ' prefix)"),
  jwts: z.array(z.string()).optional().describe("Multiple JWTs to decode at once"),
}).refine(d => d.jwt || (d.jwts?.length ?? 0) > 0, { message: "jwt or jwts required" });

toolRegistry.register({
  name: "jwt_decoder",
  description:
    "**Pure-local JWT parser** — decodes header + payload base64 of any JSON Web Token without verifying the signature. Privacy-preserving: the JWT never leaves the worker; no external API calls. Identifies: algorithm, key ID, issuer (with auto-classification: Auth0 / Okta / Cognito / Firebase / Azure AD / Google / Apple / GitHub / Supabase / Clerk / WorkOS / etc.), audience, subject, expiry status (with seconds-until-expiry), email/username claims, and any custom claims. Detects security flags: `alg=none` (CRITICAL), missing exp/iss/aud claims, very-long-lived tokens. Use whenever the agent encounters a JWT in github_code_search/postman_public_search/leaked-config results — instantly tells you what API/tenant/user it was for.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "jwt_decoder",
      input: i,
      timeoutMs: 5_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "jwt_decoder failed");
    return res.output;
  },
});
